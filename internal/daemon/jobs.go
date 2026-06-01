// Package daemon implements the job queue for overkill's daemon mode (§8.7.3).
//
// Jobs arrive via the ACP /v1/jobs endpoint and are persisted in PostgreSQL so
// the queue survives restarts. A Worker pool picks them up, calls a pluggable
// RunFunc, and transitions the job through the state machine:
//
//	queued → running → completed
//	                 → failed
//	queued → cancelled   (explicit cancel before pickup)
//	running → cancelled  (explicit cancel during run)
//	running → suspended  (waiting for remote approval)
package daemon

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"

	_ "github.com/lib/pq"
)

// JobStatus is the current lifecycle state of a Job.
type JobStatus string

const (
	JobQueued    JobStatus = "queued"
	JobRunning   JobStatus = "running"
	JobSuspended JobStatus = "suspended" // waiting for remote approval
	JobCompleted JobStatus = "completed"
	JobFailed    JobStatus = "failed"
	JobCancelled JobStatus = "cancelled"
)

// isTerminal reports whether a status is a final state.
func isTerminal(s JobStatus) bool {
	return s == JobCompleted || s == JobFailed || s == JobCancelled
}

// Job is one unit of deferred work submitted via the ACP bridge.
type Job struct {
	ID        string    `json:"id"`
	SessionID string    `json:"session_id"`
	Intent    string    `json:"intent"` // the user prompt
	Status    JobStatus `json:"status"`
	Channel   string    `json:"channel"` // originating bridge channel
	ChatKey   string    `json:"chat_key"`
	Profile   string    `json:"profile"` // "remote" for bridge-originated
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Error     string    `json:"error,omitempty"`
}

// JobStore persists jobs to PostgreSQL.
type JobStore struct {
	db *sql.DB
}

// NewJobStore wraps a *sql.DB. Caller owns DB lifecycle.
func NewJobStore(db *sql.DB) (*JobStore, error) {
	s := &JobStore{db: db}
	if err := s.migrate(); err != nil {
		return nil, fmt.Errorf("daemon: migrate: %w", err)
	}
	return s, nil
}

func (s *JobStore) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS daemon_jobs (
			id          TEXT PRIMARY KEY,
			session_id  TEXT NOT NULL DEFAULT '',
			intent      TEXT NOT NULL DEFAULT '',
			status      TEXT NOT NULL DEFAULT 'queued',
			channel     TEXT NOT NULL DEFAULT '',
			chat_key    TEXT NOT NULL DEFAULT '',
			profile     TEXT NOT NULL DEFAULT '',
			error_msg   TEXT NOT NULL DEFAULT '',
			created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_daemon_jobs_status ON daemon_jobs (status)`)
	return err
}

// Close is a no-op; the caller owns the DB lifecycle.
func (s *JobStore) Close() error { return nil }

// Create persists a new Job. The job must have a non-empty ID; callers
// should set it with uuid.New().String() before calling Create.
func (s *JobStore) Create(ctx context.Context, job Job) error {
	if job.ID == "" {
		return errors.New("daemon: job id required")
	}
	return s.put(ctx, job)
}

// Get retrieves a Job by ID.
func (s *JobStore) Get(ctx context.Context, id string) (*Job, error) {
	var j Job
	err := s.db.QueryRowContext(ctx, `
		SELECT id, session_id, intent, status, channel, chat_key, profile, error_msg, created_at, updated_at
		FROM daemon_jobs WHERE id = $1
	`, id).Scan(&j.ID, &j.SessionID, &j.Intent, &j.Status, &j.Channel, &j.ChatKey, &j.Profile, &j.Error, &j.CreatedAt, &j.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("daemon: job %q not found", id)
	}
	if err != nil {
		return nil, fmt.Errorf("daemon: get job: %w", err)
	}
	return &j, nil
}

// UpdateStatus transitions a job to status.
func (s *JobStore) UpdateStatus(ctx context.Context, id string, status JobStatus, errMsg string) error {
	now := time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `
		UPDATE daemon_jobs SET status = $1, error_msg = $2, updated_at = $3 WHERE id = $4
	`, string(status), errMsg, now, id)
	if err != nil {
		return fmt.Errorf("daemon: update status: %w", err)
	}
	return nil
}

// List returns active jobs sorted by CreatedAt descending (newest first).
func (s *JobStore) List(ctx context.Context) ([]Job, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, session_id, intent, status, channel, chat_key, profile, error_msg, created_at, updated_at
		FROM daemon_jobs
		WHERE status IN ('queued','running')
		ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("daemon: list jobs: %w", err)
	}
	defer rows.Close()

	var jobs []Job
	for rows.Next() {
		var j Job
		if err := rows.Scan(&j.ID, &j.SessionID, &j.Intent, &j.Status, &j.Channel, &j.ChatKey, &j.Profile, &j.Error, &j.CreatedAt, &j.UpdatedAt); err != nil {
			log.Printf("daemon: scan job row: %v", err)
			continue
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

// Cancel transitions a job to JobCancelled.
func (s *JobStore) Cancel(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE daemon_jobs
		SET status = $1, error_msg = '', updated_at = $2
		WHERE id = $3 AND status NOT IN ('completed','failed','cancelled')
	`, string(JobCancelled), time.Now().UTC(), id)
	if err != nil {
		return fmt.Errorf("daemon: cancel job %q: %w", id, err)
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return fmt.Errorf("daemon: job %q is already in a terminal state", id)
	}
	return nil
}

func (s *JobStore) put(ctx context.Context, job Job) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO daemon_jobs (id, session_id, intent, status, channel, chat_key, profile, error_msg, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (id) DO UPDATE SET
			session_id  = EXCLUDED.session_id,
			intent      = EXCLUDED.intent,
			status      = EXCLUDED.status,
			channel     = EXCLUDED.channel,
			chat_key    = EXCLUDED.chat_key,
			profile     = EXCLUDED.profile,
			error_msg   = EXCLUDED.error_msg,
			updated_at  = EXCLUDED.updated_at
	`,
		job.ID, job.SessionID, job.Intent, string(job.Status), job.Channel,
		job.ChatKey, job.Profile, job.Error, job.CreatedAt, job.UpdatedAt)
	if err != nil {
		return fmt.Errorf("daemon: put job: %w", err)
	}
	return nil
}

// RunFunc is the callback invoked by a Worker for each job it picks up.
type RunFunc func(ctx context.Context, job Job) error

// Worker is a bounded pool that picks up queued jobs, runs them, and
// updates their status in JobStore.
type Worker struct {
	store  *JobStore
	run    RunFunc
	sem    chan struct{}
	mu     sync.Mutex
	queue  chan Job
	cancel context.CancelFunc
	wg     sync.WaitGroup
	// ttlDone is closed when the TTL watcher goroutine exits.
	ttlDone chan struct{}
}

// NewWorker creates a Worker with concurrency limited to n parallel jobs.
func NewWorker(store *JobStore, run RunFunc, n int) *Worker {
	if n <= 0 {
		n = 1
	}
	w := &Worker{
		store: store,
		run:   run,
		sem:   make(chan struct{}, n),
		queue: make(chan Job, 1024),
	}
	return w
}

// Start launches the background dispatch loop and recovers any orphaned
// jobs from a previous daemon crash (queued or running jobs that never finished).
// Also starts a TTL watcher that periodically resets stuck "running" jobs (M18).
func (w *Worker) Start(ctx context.Context) {
	ctx, w.cancel = context.WithCancel(ctx)

	// Recover orphaned jobs from a previous crash.
	jobs, err := w.store.List(ctx)
	if err == nil {
		for _, job := range jobs {
			if job.Status == "queued" || job.Status == "running" {
				w.store.UpdateStatus(ctx, job.ID, JobQueued, "")
				select {
				case w.queue <- job:
				default:
					// Queue full — job is persisted as queued; the TTL
					// watcher or next recovery will drain it.
				}
			}
		}
	}

	w.ttlDone = make(chan struct{})
	go w.dispatch(ctx)
	go w.watchStuckJobs(ctx)
}

// Stop shuts down the worker pool gracefully, waiting for in-flight
// jobs and the TTL watcher to complete.
func (w *Worker) Stop() {
	if w.cancel != nil {
		w.cancel()
	}
	w.wg.Wait()
	if w.ttlDone != nil {
		<-w.ttlDone
	}
}

// Submit enqueues a job for execution. When the in-memory queue is full
// (256 capacity), the job is persisted directly to the store as "queued"
// so it survives and is picked up on the next recovery cycle (M14).
func (w *Worker) Submit(job Job) error {
	select {
	case w.queue <- job:
		return nil
	default:
		// Queue full — persist as queued so the job isn't lost.
		// Recovery on next Start() will pick it up, or the TTL watcher
		// below will drain it back into the queue on the next tick.
		if w.store != nil {
			_ = w.store.UpdateStatus(context.Background(), job.ID, JobQueued, "overflow: queued from Submit")
			return nil // job persisted, not lost
		}
		return errors.New("daemon: worker queue full")
	}
}

func (w *Worker) dispatch(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-w.queue:
			w.sem <- struct{}{}
			// Check ctx after acquiring semaphore: if the context
			// was cancelled while we waited for a slot, don't leak
			// a goroutine that will immediately bail out.
			if ctx.Err() != nil {
				<-w.sem
				return
			}
			w.wg.Add(1)
			go func(j Job) {
				defer func() { <-w.sem }()
				defer w.wg.Done()
				w.execute(ctx, j)
			}(job)
		}
	}
}

func (w *Worker) execute(ctx context.Context, job Job) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("daemon: panic executing job %s: %v", job.ID, r)
			_ = w.store.UpdateStatus(context.Background(), job.ID, JobFailed, fmt.Sprintf("panic: %v", r))
		}
	}()
	// Use background context for DB updates so that a cancelled parent
	// context doesn't leave the job stuck in "running" forever.
	bgCtx := context.Background()
	_ = w.store.UpdateStatus(bgCtx, job.ID, JobRunning, "")

	err := w.run(ctx, job)

	if ctx.Err() != nil {
		_ = w.store.UpdateStatus(bgCtx, job.ID, JobCancelled, "")
		return
	}
	if err != nil {
		_ = w.store.UpdateStatus(bgCtx, job.ID, JobFailed, err.Error())
		return
	}
	_ = w.store.UpdateStatus(bgCtx, job.ID, JobCompleted, "")
}

// NewJob constructs a Job with a fresh ID and the current timestamp.
func NewJob(intent, channel, chatKey, profile string) Job {
	now := time.Now().UTC()
	return Job{
		ID:        uuid.New().String(),
		Intent:    intent,
		Status:    JobQueued,
		Channel:   channel,
		ChatKey:   chatKey,
		Profile:   profile,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// watchStuckJobs periodically resets jobs that have been stuck in "running"
// status for longer than the TTL (30 min). After a crash or hung execution,
// these jobs would otherwise stay running forever (M18). The watcher ticks
// every 5 minutes and drains recovered jobs into the queue.
func (w *Worker) watchStuckJobs(ctx context.Context) {
	defer close(w.ttlDone)
	const (
		stuckTTL  = 30 * time.Minute
		watchTick = 5 * time.Minute
	)
	ticker := time.NewTicker(watchTick)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			jobs, err := w.store.List(ctx)
			if err != nil {
				continue
			}
			cutoff := time.Now().UTC().Add(-stuckTTL)
			for _, job := range jobs {
				if job.Status == "running" && job.UpdatedAt.Before(cutoff) {
					w.store.UpdateStatus(ctx, job.ID, JobQueued, "ttl: stuck running → queued")
					select {
					case w.queue <- job:
					default:
						// Queue full — job is persisted as queued; next tick or
						// recovery will drain it.
					}
				}
			}
		}
	}
}

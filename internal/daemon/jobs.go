// Package daemon implements the job queue for overkill's daemon mode (§8.7.3).
//
// Jobs arrive via the ACP /v1/jobs endpoint and are persisted in BadgerDB so
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
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/dgraph-io/badger/v4"
	"github.com/google/uuid"
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

func jobKey(id string) []byte {
	return []byte("job:" + id)
}

// JobStore persists jobs to BadgerDB.
type JobStore struct {
	db *badger.DB
}

// NewJobStore opens (or creates) the BadgerDB at dir and returns a JobStore.
// Pass an in-memory DB for tests: badger.Open(badger.DefaultOptions("").WithInMemory(true)).
func NewJobStore(db *badger.DB) *JobStore {
	return &JobStore{db: db}
}

// OpenJobStore opens a BadgerDB at dir and wraps it in a JobStore.
func OpenJobStore(dir string) (*JobStore, error) {
	db, err := badger.Open(badger.DefaultOptions(dir).WithLoggingLevel(badger.ERROR))
	if err != nil {
		return nil, fmt.Errorf("daemon: open job store: %w", err)
	}
	return NewJobStore(db), nil
}

// Close releases the underlying BadgerDB handle.
func (s *JobStore) Close() error {
	return s.db.Close()
}

// Create persists a new Job. The job must have a non-empty ID; callers
// should set it with uuid.New().String() before calling Create.
func (s *JobStore) Create(ctx context.Context, job Job) error {
	if job.ID == "" {
		return errors.New("daemon: job id required")
	}
	return s.put(job)
}

// Get retrieves a Job by ID. Returns an error wrapping badger.ErrKeyNotFound
// if the job does not exist.
func (s *JobStore) Get(ctx context.Context, id string) (*Job, error) {
	var job Job
	err := s.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get(jobKey(id))
		if err != nil {
			return err
		}
		return item.Value(func(val []byte) error {
			return json.Unmarshal(val, &job)
		})
	})
	if err != nil {
		if errors.Is(err, badger.ErrKeyNotFound) {
			return nil, fmt.Errorf("daemon: job %q not found", id)
		}
		return nil, fmt.Errorf("daemon: get job: %w", err)
	}
	return &job, nil
}

// UpdateStatus transitions a job to status. errMsg is stored in Job.Error
// when status is JobFailed; it is ignored otherwise.
func (s *JobStore) UpdateStatus(ctx context.Context, id string, status JobStatus, errMsg string) error {
	job, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	job.Status = status
	job.UpdatedAt = time.Now().UTC()
	if status == JobFailed {
		job.Error = errMsg
	}
	return s.put(*job)
}

// List returns all jobs sorted by CreatedAt descending (newest first).
func (s *JobStore) List(ctx context.Context) ([]Job, error) {
	var jobs []Job
	err := s.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		opts.Prefix = []byte("job:")
		it := txn.NewIterator(opts)
		defer it.Close()
		for it.Rewind(); it.Valid(); it.Next() {
			var j Job
			if err := it.Item().Value(func(val []byte) error {
				return json.Unmarshal(val, &j)
			}); err != nil {
				continue
			}
			jobs = append(jobs, j)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("daemon: list jobs: %w", err)
	}
	sort.Slice(jobs, func(i, k int) bool {
		return jobs[i].CreatedAt.After(jobs[k].CreatedAt)
	})
	return jobs, nil
}

// Cancel transitions a job to JobCancelled. Returns an error if the job is
// already in a terminal state.
func (s *JobStore) Cancel(ctx context.Context, id string) error {
	job, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if isTerminal(job.Status) {
		return fmt.Errorf("daemon: job %q is already %s", id, job.Status)
	}
	job.Status = JobCancelled
	job.UpdatedAt = time.Now().UTC()
	return s.put(*job)
}

func (s *JobStore) put(job Job) error {
	data, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("daemon: marshal job: %w", err)
	}
	return s.db.Update(func(txn *badger.Txn) error {
		return txn.Set(jobKey(job.ID), data)
	})
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
		queue: make(chan Job, 256),
	}
	return w
}

// Start launches the background dispatch loop. Stop via the returned cancel or
// by cancelling the parent context.
func (w *Worker) Start(ctx context.Context) {
	ctx, w.cancel = context.WithCancel(ctx)
	go w.dispatch(ctx)
}

// Stop shuts down the worker pool gracefully.
func (w *Worker) Stop() {
	if w.cancel != nil {
		w.cancel()
	}
}

// Submit enqueues a job for execution. The job must already exist in JobStore.
// Submit is non-blocking; it returns an error only when the internal queue is
// full (capacity 256).
func (w *Worker) Submit(job Job) error {
	select {
	case w.queue <- job:
		return nil
	default:
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
			go func(j Job) {
				defer func() { <-w.sem }()
				w.execute(ctx, j)
			}(job)
		}
	}
}

func (w *Worker) execute(ctx context.Context, job Job) {
	_ = w.store.UpdateStatus(ctx, job.ID, JobRunning, "")

	err := w.run(ctx, job)

	if ctx.Err() != nil {
		_ = w.store.UpdateStatus(ctx, job.ID, JobCancelled, "")
		return
	}
	if err != nil {
		_ = w.store.UpdateStatus(ctx, job.ID, JobFailed, err.Error())
		return
	}
	_ = w.store.UpdateStatus(ctx, job.ID, JobCompleted, "")
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

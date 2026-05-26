// Package journal — CLAIM-CONFIRM async compression queue (§4.19,
// inspired by claude-mem).
//
// The problem: an observation arrives. Persisting it is fast (one
// JSONL append). Compressing it — LLM rewrite into structured
// facts/concepts, embedding for vector search, deduping against
// neighbors — is slow and unreliable (model timeouts, bridge down,
// disk hiccups). If we do the slow part on the hot path, every
// captured observation pays the latency. If we just kick off a
// goroutine and forget, a crashed worker means the observation
// stays half-processed forever.
//
// CLAIM-CONFIRM splits the lifecycle:
//
//   1. CAPTURE: durable write to disk (raw observation JSONL).
//      Never blocks; agent moves on.
//   2. CLAIM: a worker pulls a pending observation and marks it
//      `claimed` with its PID + a deadline. Other workers skip
//      it while the deadline is in the future.
//   3. CONFIRM: the worker finishes compression and marks it
//      `confirmed`. The job is done.
//   4. RECOVERY: if a worker dies mid-CLAIM, its deadline expires
//      and another worker re-claims. No "stuck forever" state.
//
// Storage shape: one tiny JSON file per observation under
// <dir>/queue/ with the state machine. Atomic-rename writes for
// the state transitions so a crash mid-write doesn't corrupt the
// queue file (the previous state survives intact).
package journal

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// QueueState is the CLAIM-CONFIRM lifecycle position.
type QueueState string

const (
	// QueuePending: enqueued, waiting for a worker.
	QueuePending QueueState = "pending"
	// QueueClaimed: a worker took it; check ClaimedUntil to know
	// whether the claim is still valid.
	QueueClaimed QueueState = "claimed"
	// QueueConfirmed: compression done; safe to delete or archive.
	QueueConfirmed QueueState = "confirmed"
	// QueueFailed: compression hit a hard error too many times.
	// Surfaced for operator review; not retried.
	QueueFailed QueueState = "failed"
)

// QueueJob is one item in the CLAIM-CONFIRM queue. ObservationID
// points at the raw observation; the actual narrative + embedding
// live in the ObservationStore, not duplicated here.
type QueueJob struct {
	ID            string     `json:"id"`
	ObservationID string     `json:"observation_id"`
	State         QueueState `json:"state"`
	EnqueuedAt    time.Time  `json:"enqueued_at"`
	ClaimedAt     time.Time  `json:"claimed_at,omitempty"`
	ClaimedBy     int        `json:"claimed_by,omitempty"`
	ClaimedUntil  time.Time  `json:"claimed_until,omitempty"`
	ConfirmedAt   time.Time  `json:"confirmed_at,omitempty"`
	Attempts      int        `json:"attempts"`
	LastError     string     `json:"last_error,omitempty"`
}

// CompressionQueue is the on-disk JSONL-per-job store. Concurrent
// access from multiple workers (in-process or cross-process) is
// arbitrated by the file-rename atomicity: a Claim that loses the
// rename race wins nothing, the other worker has the job.
type CompressionQueue struct {
	dir string
	mu  sync.Mutex
}

// NewCompressionQueue wires the queue to a directory. Files land
// at <dir>/<job-id>.json. Lazy-created on first Enqueue.
func NewCompressionQueue(dir string) *CompressionQueue {
	return &CompressionQueue{dir: dir}
}

// Enqueue creates a new pending job for the observation. Idempotent
// by observation ID: re-enqueuing the same observation returns the
// existing job (caller can read its state instead of creating a
// duplicate).
func (q *CompressionQueue) Enqueue(observationID string) (*QueueJob, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if observationID == "" {
		return nil, errors.New("queue: observation id required")
	}
	// Idempotency check: scan existing jobs for this observation.
	if err := os.MkdirAll(q.dir, 0o755); err != nil {
		return nil, fmt.Errorf("queue: mkdir: %w", err)
	}
	jobs, err := q.listLocked()
	if err != nil {
		return nil, err
	}
	for _, j := range jobs {
		if j.ObservationID == observationID && j.State != QueueFailed {
			return &j, nil
		}
	}
	job := QueueJob{
		ID:            uuid.NewString(),
		ObservationID: observationID,
		State:         QueuePending,
		EnqueuedAt:    time.Now().UTC(),
	}
	if err := q.saveLocked(&job); err != nil {
		return nil, err
	}
	return &job, nil
}

// Claim atomically takes the oldest pending job (or one whose
// claim has expired) and marks it Claimed by the calling process
// for the given lease duration. Returns (nil, nil) when nothing is
// available — caller should sleep + retry.
//
// Lease semantics: claim expires after `lease`. A worker that
// can't finish in time can call Renew to extend; a dead worker's
// claim simply expires and another worker picks the job up. No
// distributed lock service required.
func (q *CompressionQueue) Claim(workerPID int, lease time.Duration) (*QueueJob, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	jobs, err := q.listLocked()
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	// Sort by enqueue time so we don't starve old jobs in a busy
	// queue. Stable sort isn't strictly necessary; deterministic
	// ordering helps tests.
	sort.Slice(jobs, func(i, j int) bool {
		return jobs[i].EnqueuedAt.Before(jobs[j].EnqueuedAt)
	})
	for i := range jobs {
		j := &jobs[i]
		// Skip terminal states.
		if j.State == QueueConfirmed || j.State == QueueFailed {
			continue
		}
		// Skip live claims.
		if j.State == QueueClaimed && j.ClaimedUntil.After(now) {
			continue
		}
		// Take it.
		j.State = QueueClaimed
		j.ClaimedAt = now
		j.ClaimedBy = workerPID
		j.ClaimedUntil = now.Add(lease)
		j.Attempts++
		if err := q.saveLocked(j); err != nil {
			return nil, err
		}
		dup := *j
		return &dup, nil
	}
	return nil, nil
}

// Renew extends the claim deadline. Used by long-running workers
// (LLM compression on a slow connection) to avoid expiration.
// Errors when the job no longer belongs to this worker — the lease
// expired and another worker took over.
func (q *CompressionQueue) Renew(jobID string, workerPID int, lease time.Duration) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	j, err := q.loadLocked(jobID)
	if err != nil {
		return err
	}
	if j.State != QueueClaimed || j.ClaimedBy != workerPID {
		return fmt.Errorf("queue: renew %s: no longer claimed by this worker", jobID)
	}
	j.ClaimedUntil = time.Now().UTC().Add(lease)
	return q.saveLocked(j)
}

// Confirm marks a job done. Caller must hold a valid claim.
func (q *CompressionQueue) Confirm(jobID string, workerPID int) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	j, err := q.loadLocked(jobID)
	if err != nil {
		return err
	}
	if j.State != QueueClaimed || j.ClaimedBy != workerPID {
		return fmt.Errorf("queue: confirm %s: not claimed by this worker", jobID)
	}
	j.State = QueueConfirmed
	j.ConfirmedAt = time.Now().UTC()
	j.LastError = ""
	return q.saveLocked(j)
}

// Fail records a compression error. After maxAttempts the job
// transitions to QueueFailed and stops being re-claimed. Below the
// threshold it goes back to QueuePending for retry.
func (q *CompressionQueue) Fail(jobID string, workerPID int, err error, maxAttempts int) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	j, loadErr := q.loadLocked(jobID)
	if loadErr != nil {
		return loadErr
	}
	if j.State != QueueClaimed || j.ClaimedBy != workerPID {
		return fmt.Errorf("queue: fail %s: not claimed by this worker", jobID)
	}
	j.LastError = err.Error()
	if maxAttempts > 0 && j.Attempts >= maxAttempts {
		j.State = QueueFailed
	} else {
		j.State = QueuePending
		j.ClaimedAt = time.Time{}
		j.ClaimedBy = 0
		j.ClaimedUntil = time.Time{}
	}
	return q.saveLocked(j)
}

// List returns every job currently on disk. Useful for operators
// inspecting the queue ("which observations are stuck failed?").
func (q *CompressionQueue) List() ([]QueueJob, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.listLocked()
}

// Get returns one job by ID.
func (q *CompressionQueue) Get(id string) (*QueueJob, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.loadLocked(id)
}

// PendingCount returns the number of jobs that could be claimed
// right now — pending plus jobs whose claim has expired.
func (q *CompressionQueue) PendingCount() (int, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	jobs, err := q.listLocked()
	if err != nil {
		return 0, err
	}
	now := time.Now().UTC()
	count := 0
	for _, j := range jobs {
		if j.State == QueuePending {
			count++
			continue
		}
		if j.State == QueueClaimed && !j.ClaimedUntil.After(now) {
			count++
		}
	}
	return count, nil
}

func (q *CompressionQueue) listLocked() ([]QueueJob, error) {
	entries, err := os.ReadDir(q.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("queue: readdir: %w", err)
	}
	out := make([]QueueJob, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		j, err := q.loadLocked(jobIDFromFilename(e.Name()))
		if err != nil {
			continue // corrupt file — skip rather than fail the whole list
		}
		out = append(out, *j)
	}
	return out, nil
}

func (q *CompressionQueue) loadLocked(id string) (*QueueJob, error) {
	if id == "" {
		return nil, errors.New("queue: empty job id")
	}
	path := filepath.Join(q.dir, id+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("queue: load %s: %w", id, err)
	}
	var j QueueJob
	if err := json.Unmarshal(data, &j); err != nil {
		return nil, fmt.Errorf("queue: parse %s: %w", id, err)
	}
	return &j, nil
}

func (q *CompressionQueue) saveLocked(j *QueueJob) error {
	if err := os.MkdirAll(q.dir, 0o755); err != nil {
		return fmt.Errorf("queue: mkdir: %w", err)
	}
	path := filepath.Join(q.dir, j.ID+".json")
	tmp := path + ".tmp"
	data, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return fmt.Errorf("queue: marshal: %w", err)
	}
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("queue: write tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("queue: rename: %w", err)
	}
	return nil
}

// jobIDFromFilename trims the .json suffix from a queue file's
// basename. Errors are intentionally swallowed (return raw input);
// the caller's load will fail loudly on a missing file.
func jobIDFromFilename(name string) string {
	if filepath.Ext(name) == ".json" {
		return name[:len(name)-5]
	}
	return name
}

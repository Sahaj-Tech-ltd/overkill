// Package automation — background task ledger (master plan §7.1).
//
// Records every long-running operation (cron job, SOP step, sub-agent run,
// daemon-fired alarm) with a lifecycle: queued → running → completed | failed
// | cancelled. The ledger is the single observability surface for "what did
// my agent do while I was AFK?".
//
// Implementation goals:
//   - in-memory by default; persist via SetStore when wired to Badger
//   - thread-safe; cheap to update on every state transition
//   - bounded — keep last N entries in memory, evict oldest
package automation

import (
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// TaskState is the lifecycle position of a task in the ledger.
type TaskState string

const (
	TaskQueued    TaskState = "queued"
	TaskRunning   TaskState = "running"
	TaskCompleted TaskState = "completed"
	TaskFailed    TaskState = "failed"
	TaskCancelled TaskState = "cancelled"
	// TaskTimedOut means the task hit its complexity-derived timeout
	// (§7.1 Task Flow) and was interrupted with state saved. A follow-
	// up alarm can resume it.
	TaskTimedOut TaskState = "timed_out"
	// TaskLost means the runtime that owned this task disappeared
	// without reporting an exit state. Sweeper sets this after a
	// grace period when the owner PID is no longer alive AND the task
	// hasn't heartbeated.
	TaskLost TaskState = "lost"
)

// terminalState reports whether a state is a final resting state.
// Centralized so eviction + sweeper logic agree on what counts.
func terminalState(s TaskState) bool {
	switch s {
	case TaskCompleted, TaskFailed, TaskCancelled, TaskTimedOut, TaskLost:
		return true
	}
	return false
}

// LedgerTask is a single recorded background operation.
type LedgerTask struct {
	ID        string         `json:"id"`
	Source    string         `json:"source"` // "cron" | "sop" | "subagent" | "alarm" | ...
	Name      string         `json:"name"`
	State     TaskState      `json:"state"`
	StartedAt time.Time      `json:"started_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	EndedAt   time.Time      `json:"ended_at,omitempty"`
	Result    string         `json:"result,omitempty"`
	Error     string         `json:"error,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	// OwnerPID is the process that started this task. Sweeper uses it
	// to distinguish "task is genuinely stuck" from "task is fine, my
	// daemon just hasn't seen a heartbeat yet". When 0, the sweeper
	// treats UpdatedAt as the sole liveness signal.
	OwnerPID int `json:"owner_pid,omitempty"`
}

// Ledger is the in-memory store of LedgerTasks. Bounded by maxEntries; the
// oldest completed/failed/cancelled rows are evicted when the cap is hit.
type Ledger struct {
	mu         sync.RWMutex
	tasks      map[string]*LedgerTask
	maxEntries int
}

// NewLedger creates an in-memory ledger that retains up to maxEntries rows.
func NewLedger(maxEntries int) *Ledger {
	if maxEntries <= 0 {
		maxEntries = 200
	}
	return &Ledger{tasks: map[string]*LedgerTask{}, maxEntries: maxEntries}
}

// Begin records a new queued/running task and returns its ID.
func (l *Ledger) Begin(source, name string) *LedgerTask {
	return l.BeginOwned(source, name, 0)
}

// BeginOwned is Begin with an explicit owner PID. Pass os.Getpid() for
// tasks driven by the current process; pass 0 when the owner isn't
// known (e.g. cron jobs which use timing as the only liveness signal).
func (l *Ledger) BeginOwned(source, name string, ownerPID int) *LedgerTask {
	now := time.Now().UTC()
	t := &LedgerTask{
		ID:        uuid.NewString(),
		Source:    source,
		Name:      name,
		State:     TaskRunning,
		StartedAt: now,
		UpdatedAt: now,
		OwnerPID:  ownerPID,
	}
	l.mu.Lock()
	l.tasks[t.ID] = t
	l.evictLocked()
	l.mu.Unlock()
	return t
}

// Heartbeat bumps the task's UpdatedAt without changing state. Used by
// long-running tasks to signal "still alive" so the sweeper doesn't
// mark them lost. No-op for unknown IDs and terminal-state tasks.
func (l *Ledger) Heartbeat(id string) {
	l.HeartbeatAt(id, time.Now().UTC())
}

// HeartbeatAt is Heartbeat with an explicit timestamp. Exposed so tests
// can drive heartbeats on the same fake clock as the sweeper; callers
// outside tests should use Heartbeat.
func (l *Ledger) HeartbeatAt(id string, when time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	t, ok := l.tasks[id]
	if !ok || terminalState(t.State) {
		return
	}
	t.UpdatedAt = when
}

// Update mutates state on an in-flight task. No-op when ID is unknown.
func (l *Ledger) Update(id string, state TaskState, result string, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	t, ok := l.tasks[id]
	if !ok {
		return
	}
	t.State = state
	t.Result = result
	if err != nil {
		t.Error = err.Error()
	}
	t.UpdatedAt = time.Now().UTC()
	if terminalState(state) {
		t.EndedAt = t.UpdatedAt
	}
}

// TimedOut marks the task as interrupted by its time budget. Distinct
// from Fail because the task's state is recoverable — Task Flow (§7.1
// Layer 7) can resume from the last checkpoint when an alarm fires.
// Reason goes into Result so resume callers can inspect the budget
// signal without it looking like a failure-class error.
func (l *Ledger) TimedOut(id, reason string) { l.Update(id, TaskTimedOut, reason, nil) }

// MarkLost flips a stuck task to TaskLost. Used by the sweeper; not
// meant to be called from task implementations themselves. Reason is
// recorded as an error because `lost` is a failure-class outcome —
// the user's UI surfaces it the same way as Fail.
func (l *Ledger) MarkLost(id, reason string) {
	l.Update(id, TaskLost, "", errors.New(reason))
}

// Complete is a convenience wrapper for state=Completed with no error.
func (l *Ledger) Complete(id, result string) { l.Update(id, TaskCompleted, result, nil) }

// Fail is a convenience wrapper for state=Failed.
func (l *Ledger) Fail(id string, err error) { l.Update(id, TaskFailed, "", err) }

// Cancel marks the task cancelled.
func (l *Ledger) Cancel(id string) { l.Update(id, TaskCancelled, "", nil) }

// Get returns a copy of one task.
func (l *Ledger) Get(id string) (LedgerTask, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	t, ok := l.tasks[id]
	if !ok {
		return LedgerTask{}, false
	}
	return *t, true
}

// List returns all tasks newest-first.
func (l *Ledger) List() []LedgerTask {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make([]LedgerTask, 0, len(l.tasks))
	for _, t := range l.tasks {
		out = append(out, *t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out
}

// Active returns tasks still in queued/running.
func (l *Ledger) Active() []LedgerTask {
	all := l.List()
	out := make([]LedgerTask, 0, len(all))
	for _, t := range all {
		if t.State == TaskQueued || t.State == TaskRunning {
			out = append(out, t)
		}
	}
	return out
}

// evictLocked drops the oldest terminal-state rows until len ≤ maxEntries.
// Caller MUST hold l.mu.
func (l *Ledger) evictLocked() {
	if len(l.tasks) <= l.maxEntries {
		return
	}
	type row struct {
		id string
		ts time.Time
	}
	terminal := make([]row, 0, len(l.tasks))
	for _, t := range l.tasks {
		if terminalState(t.State) {
			terminal = append(terminal, row{t.ID, t.EndedAt})
		}
	}
	sort.Slice(terminal, func(i, j int) bool { return terminal[i].ts.Before(terminal[j].ts) })
	for i := 0; i < len(terminal) && len(l.tasks) > l.maxEntries; i++ {
		delete(l.tasks, terminal[i].id)
	}
}

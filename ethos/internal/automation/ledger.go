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
)

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
	t := &LedgerTask{
		ID:        uuid.NewString(),
		Source:    source,
		Name:      name,
		State:     TaskRunning,
		StartedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
	}
	l.mu.Lock()
	l.tasks[t.ID] = t
	l.evictLocked()
	l.mu.Unlock()
	return t
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
	if state == TaskCompleted || state == TaskFailed || state == TaskCancelled {
		t.EndedAt = t.UpdatedAt
	}
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
		if t.State == TaskCompleted || t.State == TaskFailed || t.State == TaskCancelled {
			terminal = append(terminal, row{t.ID, t.EndedAt})
		}
	}
	sort.Slice(terminal, func(i, j int) bool { return terminal[i].ts.Before(terminal[j].ts) })
	for i := 0; i < len(terminal) && len(l.tasks) > l.maxEntries; i++ {
		delete(l.tasks, terminal[i].id)
	}
}

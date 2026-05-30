// Package tasks — cross-session task graph (§8.3 Phase 5 #2).
//
// The agent tracks user requests that span sessions: "fix the auth
// bug" from Tuesday, "add CSRF protection" from yesterday. Each
// request becomes a Task with a status (open / shipped / abandoned)
// and a list of linked commits. At session open the agent reads the
// open threads and surfaces them — "you asked me to fix X 3 days
// ago, that shipped (abc123)".
//
// Storage: one JSON file per task at ~/.overkill/tasks/<id>.json.
// Atomic temp+rename so a crash mid-write leaves the prior state
// intact. Single-file-per-task is simpler than JSONL when we need
// to mutate status / link commits — no fold-from-zero on every
// read.
//
// Linking: tasks are linked to commits via two paths:
//
//   - Trailer match: the agent inserts `Overkill-Task: <id>` in
//     commit messages. The boot scan reads `git log --grep` for
//     the trailer.
//   - Keyword scan: the agent can call LinkCommit explicitly with
//     a known SHA when it recognizes the work that just shipped.
//
// Either path is best-effort. The git surface lives outside this
// package — keeps internal/tasks portable + testable without git.
package tasks

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/atomicfile"
	"github.com/google/uuid"
)

// Status is the current lifecycle position of a Task.
type Status string

const (
	// StatusOpen: user asked, agent hasn't shipped yet.
	StatusOpen Status = "open"
	// StatusInProgress: agent is actively working on it.
	StatusInProgress Status = "in_progress"
	// StatusShipped: agent considers it done. Commits attached.
	StatusShipped Status = "shipped"
	// StatusAbandoned: user / agent decided not to do it.
	StatusAbandoned Status = "abandoned"
)

// Task is one cross-session task record.
type Task struct {
	ID         string    `json:"id"`
	SessionID  string    `json:"session_id"`
	Intent     string    `json:"intent"`
	Status     Status    `json:"status"`
	Commits    []string  `json:"commits,omitempty"`
	Tags       []string  `json:"tags,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
	ResolvedAt time.Time `json:"resolved_at,omitempty"`
	// Notes is free-form prose the agent attaches mid-flight:
	// "tried X, it failed because Y; switching to Z". Useful for
	// the session-open surface even before commits land.
	Notes string `json:"notes,omitempty"`
}

// IsTerminal reports whether the task is in a final state.
func (t *Task) IsTerminal() bool {
	return t.Status == StatusShipped || t.Status == StatusAbandoned
}

// Store wraps the on-disk tasks directory.
type Store struct {
	dir string
	mu  sync.Mutex
}

// NewStore wires the store to dir. Lazy-creates the directory on
// the first write.
func NewStore(dir string) *Store {
	return &Store{dir: dir}
}

// Open creates a new task in StatusOpen. Returns the task with its
// assigned ID. Empty intent is rejected (a task without a
// distilled intent isn't useful).
func (s *Store) Open(sessionID, intent string) (*Task, error) {
	intent = strings.TrimSpace(intent)
	if intent == "" {
		return nil, errors.New("tasks: intent required")
	}
	now := time.Now().UTC()
	t := &Task{
		ID:        uuid.NewString(),
		SessionID: sessionID,
		Intent:    intent,
		Status:    StatusOpen,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.save(t); err != nil {
		return nil, err
	}
	return t, nil
}

// Get returns the task by ID, or (nil, nil) when not found.
func (s *Store) Get(id string) (*Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked(id)
}

// SetStatus transitions the task. Idempotent. Pass shipped → also
// stamps ResolvedAt.
func (s *Store) SetStatus(id string, status Status) (*Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, err := s.loadLocked(id)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, fmt.Errorf("tasks: %q not found", id)
	}
	t.Status = status
	t.UpdatedAt = time.Now().UTC()
	if status == StatusShipped || status == StatusAbandoned {
		t.ResolvedAt = t.UpdatedAt
	}
	if err := s.saveLocked(t); err != nil {
		return nil, err
	}
	return t, nil
}

// LinkCommit attaches a commit SHA to the task. Idempotent —
// re-linking the same SHA is a no-op. Does NOT auto-transition to
// shipped; the caller decides whether one linked commit is enough.
func (s *Store) LinkCommit(id, sha string) (*Task, error) {
	sha = strings.TrimSpace(sha)
	if sha == "" {
		return nil, errors.New("tasks: commit sha required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t, err := s.loadLocked(id)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, fmt.Errorf("tasks: %q not found", id)
	}
	for _, existing := range t.Commits {
		if existing == sha {
			return t, nil
		}
	}
	t.Commits = append(t.Commits, sha)
	t.UpdatedAt = time.Now().UTC()
	if err := s.saveLocked(t); err != nil {
		return nil, err
	}
	return t, nil
}

// AppendNote attaches a one-line update to the task's Notes field.
// Multiple notes are separated by " | " so the field stays a single
// JSON string (no schema churn). Returns an error for empty input (B116).
func (s *Store) AppendNote(id, note string) (*Task, error) {
	note = strings.TrimSpace(note)
	if note == "" {
		return nil, fmt.Errorf("tasks: note must be non-empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t, err := s.loadLocked(id)
	if err != nil {
		return nil, err
	}
	if t == nil {
		return nil, fmt.Errorf("tasks: %q not found", id)
	}
	if t.Notes == "" {
		t.Notes = note
	} else {
		t.Notes = t.Notes + " | " + note
	}
	t.UpdatedAt = time.Now().UTC()
	if err := s.saveLocked(t); err != nil {
		return nil, err
	}
	return t, nil
}

// All returns every persisted task, newest-first by CreatedAt.
func (s *Store) All() ([]*Task, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.listLocked()
}

// Open returns tasks in StatusOpen or StatusInProgress, newest-first.
// Used at session open to surface unfinished threads.
func (s *Store) OpenTasks() ([]*Task, error) {
	all, err := s.All()
	if err != nil {
		return nil, err
	}
	out := all[:0]
	for _, t := range all {
		if !t.IsTerminal() {
			out = append(out, t)
		}
	}
	return out, nil
}

// OpenOlderThan returns non-terminal tasks created more than `age`
// ago. The session-open surface uses this to filter out things the
// user just asked about (no point reminding them about a request
// from 5 minutes ago).
func (s *Store) OpenOlderThan(age time.Duration) ([]*Task, error) {
	open, err := s.OpenTasks()
	if err != nil {
		return nil, err
	}
	cutoff := time.Now().UTC().Add(-age)
	out := open[:0]
	for _, t := range open {
		if t.CreatedAt.Before(cutoff) {
			out = append(out, t)
		}
	}
	return out, nil
}

// Search substring-matches across Intent / Notes / Tags
// case-insensitively. Empty query returns empty.
func (s *Store) Search(query string) ([]*Task, error) {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return nil, nil
	}
	all, err := s.All()
	if err != nil {
		return nil, err
	}
	var out []*Task
	for _, t := range all {
		if strings.Contains(strings.ToLower(t.Intent), query) ||
			strings.Contains(strings.ToLower(t.Notes), query) {
			out = append(out, t)
			continue
		}
		for _, tag := range t.Tags {
			if strings.Contains(strings.ToLower(tag), query) {
				out = append(out, t)
				break
			}
		}
	}
	return out, nil
}

// ── internals ───────────────────────────────────────────────────────

func (s *Store) save(t *Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked(t)
}

func (s *Store) saveLocked(t *Task) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("tasks: mkdir: %w", err)
	}
	path := filepath.Join(s.dir, t.ID+".json")
	data, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return fmt.Errorf("tasks: marshal: %w", err)
	}
	if err := atomicfile.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("tasks: write: %w", err)
	}
	return nil
}

func (s *Store) loadLocked(id string) (*Task, error) {
	if id == "" {
		return nil, errors.New("tasks: empty id")
	}
	path := filepath.Join(s.dir, id+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("tasks: read: %w", err)
	}
	var t Task
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("tasks: parse %s: %w", id, err)
	}
	return &t, nil
}

func (s *Store) listLocked() ([]*Task, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("tasks: readdir: %w", err)
	}
	out := make([]*Task, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		t, err := s.loadLocked(id)
		if err != nil {
			continue // skip corrupt file rather than fail the whole list
		}
		if t != nil {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

// FormatOpenerSummary renders the boot-time message the agent
// surfaces when there are stale open tasks. Empty when nothing
// qualifies. Caller decides the age threshold via OpenOlderThan.
func FormatOpenerSummary(open []*Task) string {
	if len(open) == 0 {
		return ""
	}
	var b strings.Builder
	if len(open) == 1 {
		b.WriteString("You have 1 open thread from a past session:\n")
	} else {
		fmt.Fprintf(&b, "You have %d open threads from past sessions:\n", len(open))
	}
	for _, t := range open {
		age := time.Since(t.CreatedAt).Round(time.Hour)
		fmt.Fprintf(&b, "  - %s [%s, %s ago]", t.Intent, t.Status, age)
		if len(t.Commits) > 0 {
			b.WriteString(" — commits: ")
			b.WriteString(strings.Join(t.Commits, ", "))
		}
		b.WriteByte('\n')
	}
	b.WriteString("\nReference these with `task_status <id>` or close stale ones with `task_close <id>`.")
	return b.String()
}

// ExportJSONL writes every persisted task as one JSONL line per
// task to w. Used by sync / backup. Newest-first.
func (s *Store) ExportJSONL(w *bufio.Writer) error {
	all, err := s.All()
	if err != nil {
		return err
	}
	for _, t := range all {
		data, err := json.Marshal(t)
		if err != nil {
			return err
		}
		if _, err := w.Write(append(data, '\n')); err != nil {
			return err
		}
	}
	return w.Flush()
}

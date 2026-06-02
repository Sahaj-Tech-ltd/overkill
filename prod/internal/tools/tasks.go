// Package tools — task_open / task_close / task_link_commit /
// task_note / task_list lets the agent maintain a cross-session
// task graph (§8.3 Phase 5 #2).
//
// Why typed tools instead of letting the agent edit a markdown file:
// the task graph is the surface that lets the agent surface "you
// asked me to fix X 3 days ago" at session open. A hallucinating
// turn rewriting the file could mass-close real open work or
// invent ghost tasks. The protected-path gate blocks raw writes;
// these tools provide the structured mutation channel.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/tasks"
)

// TasksStore is the minimal surface the tools need. Keeps the
// import boundary clean for testing.
type TasksStore interface {
	Open(sessionID, intent string) (*tasks.Task, error)
	Get(id string) (*tasks.Task, error)
	SetStatus(id string, status tasks.Status) (*tasks.Task, error)
	LinkCommit(id, sha string) (*tasks.Task, error)
	AppendNote(id, note string) (*tasks.Task, error)
	All() ([]*tasks.Task, error)
	OpenTasks() ([]*tasks.Task, error)
}

// ── task_open ───────────────────────────────────────────────────────

type TaskOpenTool struct {
	store    TasksStore
	provider SessionIDProvider
}

func NewTaskOpenTool(store TasksStore, p SessionIDProvider) *TaskOpenTool {
	return &TaskOpenTool{store: store, provider: p}
}
func (t *TaskOpenTool) Name() string { return "task_open" }

type taskOpenInput struct {
	Intent string `json:"intent"`
}

func (t *TaskOpenTool) Execute(_ context.Context, in json.RawMessage) (json.RawMessage, error) {
	if t.store == nil {
		return errorJSON("tasks store not configured"), nil
	}
	var req taskOpenInput
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, fmt.Errorf("task_open: %w", err)
	}
	if strings.TrimSpace(req.Intent) == "" {
		return errorJSON("task_open: 'intent' is required"), nil
	}
	sid := ""
	if t.provider != nil {
		sid = t.provider.SessionID()
	}
	task, err := t.store.Open(sid, req.Intent)
	if err != nil {
		return errorJSON(err.Error()), nil
	}
	return json.Marshal(task)
}

// ── task_close ──────────────────────────────────────────────────────

type TaskCloseTool struct{ store TasksStore }

func NewTaskCloseTool(store TasksStore) *TaskCloseTool {
	return &TaskCloseTool{store: store}
}
func (t *TaskCloseTool) Name() string { return "task_close" }

type taskCloseInput struct {
	ID        string `json:"id"`
	Abandoned bool   `json:"abandoned,omitempty"` // false = shipped (default)
}

func (t *TaskCloseTool) Execute(_ context.Context, in json.RawMessage) (json.RawMessage, error) {
	if t.store == nil {
		return errorJSON("tasks store not configured"), nil
	}
	var req taskCloseInput
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, fmt.Errorf("task_close: %w", err)
	}
	if req.ID == "" {
		return errorJSON("task_close: 'id' is required"), nil
	}
	status := tasks.StatusShipped
	if req.Abandoned {
		status = tasks.StatusAbandoned
	}
	task, err := t.store.SetStatus(req.ID, status)
	if err != nil {
		return errorJSON(err.Error()), nil
	}
	return json.Marshal(task)
}

// ── task_link_commit ────────────────────────────────────────────────

type TaskLinkCommitTool struct{ store TasksStore }

func NewTaskLinkCommitTool(store TasksStore) *TaskLinkCommitTool {
	return &TaskLinkCommitTool{store: store}
}
func (t *TaskLinkCommitTool) Name() string { return "task_link_commit" }

type taskLinkCommitInput struct {
	ID  string `json:"id"`
	SHA string `json:"sha"`
}

func (t *TaskLinkCommitTool) Execute(_ context.Context, in json.RawMessage) (json.RawMessage, error) {
	if t.store == nil {
		return errorJSON("tasks store not configured"), nil
	}
	var req taskLinkCommitInput
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, fmt.Errorf("task_link_commit: %w", err)
	}
	if req.ID == "" || req.SHA == "" {
		return errorJSON("task_link_commit: 'id' and 'sha' are required"), nil
	}
	task, err := t.store.LinkCommit(req.ID, req.SHA)
	if err != nil {
		return errorJSON(err.Error()), nil
	}
	return json.Marshal(task)
}

// ── task_note ───────────────────────────────────────────────────────

type TaskNoteTool struct{ store TasksStore }

func NewTaskNoteTool(store TasksStore) *TaskNoteTool { return &TaskNoteTool{store: store} }
func (t *TaskNoteTool) Name() string                 { return "task_note" }

type taskNoteInput struct {
	ID   string `json:"id"`
	Note string `json:"note"`
}

func (t *TaskNoteTool) Execute(_ context.Context, in json.RawMessage) (json.RawMessage, error) {
	if t.store == nil {
		return errorJSON("tasks store not configured"), nil
	}
	var req taskNoteInput
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, fmt.Errorf("task_note: %w", err)
	}
	if req.ID == "" || strings.TrimSpace(req.Note) == "" {
		return errorJSON("task_note: 'id' and 'note' are required"), nil
	}
	task, err := t.store.AppendNote(req.ID, req.Note)
	if err != nil {
		return errorJSON(err.Error()), nil
	}
	return json.Marshal(task)
}

// ── task_list ───────────────────────────────────────────────────────

type TaskListTool struct{ store TasksStore }

func NewTaskListTool(store TasksStore) *TaskListTool { return &TaskListTool{store: store} }
func (t *TaskListTool) Name() string                 { return "task_list" }

type taskListInput struct {
	// OpenOnly filters to non-terminal tasks. Default false (all).
	OpenOnly bool `json:"open_only,omitempty"`
	// Limit caps the response. Default 20.
	Limit int `json:"limit,omitempty"`
}

func (t *TaskListTool) Execute(_ context.Context, in json.RawMessage) (json.RawMessage, error) {
	if t.store == nil {
		return errorJSON("tasks store not configured"), nil
	}
	var req taskListInput
	if len(in) > 0 {
		if err := json.Unmarshal(in, &req); err != nil {
			return nil, fmt.Errorf("task_list: %w", err)
		}
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 20
	}
	var (
		list []*tasks.Task
		err  error
	)
	if req.OpenOnly {
		list, err = t.store.OpenTasks()
	} else {
		list, err = t.store.All()
	}
	if err != nil {
		return errorJSON(err.Error()), nil
	}
	if len(list) > limit {
		list = list[:limit]
	}
	return json.Marshal(map[string]any{
		"tasks": list,
		"count": len(list),
		"as_of": time.Now().UTC(),
	})
}

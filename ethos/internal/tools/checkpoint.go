// Package tools — checkpoint_snapshot / checkpoint_list / checkpoint_restore
// surface internal/checkpoint to the agent (master plan §4.8).
//
// Use case: before any destructive operation (patch, fs_write, rm) the agent
// can call checkpoint_snapshot with the affected paths. If anything goes
// wrong, the user runs `/rollback <id>` (or the agent invokes
// checkpoint_restore directly).
package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Sahaj-Tech-ltd/overkill/internal/checkpoint"
)

// CheckpointSnapshotTool captures the current contents of one or more files.
type CheckpointSnapshotTool struct {
	mgr        *checkpoint.Manager
	sessionFn  func() string
}

func NewCheckpointSnapshotTool(m *checkpoint.Manager, sessionFn func() string) *CheckpointSnapshotTool {
	return &CheckpointSnapshotTool{mgr: m, sessionFn: sessionFn}
}

func (t *CheckpointSnapshotTool) Name() string { return "checkpoint_snapshot" }

type checkpointSnapshotInput struct {
	Paths  []string `json:"paths"`
	Reason string   `json:"reason,omitempty"`
}

func (t *CheckpointSnapshotTool) Execute(ctx context.Context, in json.RawMessage) (json.RawMessage, error) {
	if t.mgr == nil {
		return errorJSON("checkpoint manager not configured"), nil
	}
	var req checkpointSnapshotInput
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, fmt.Errorf("checkpoint_snapshot: %w", err)
	}
	if len(req.Paths) == 0 {
		return errorJSON("paths is required"), nil
	}
	sid := ""
	if t.sessionFn != nil {
		sid = t.sessionFn()
	}
	man, err := t.mgr.Snapshot(sid, req.Reason, req.Paths)
	if err != nil {
		return errorJSON(err.Error()), nil
	}
	out, _ := json.Marshal(map[string]any{
		"id":         man.ID,
		"entries":    len(man.Entries),
		"created_at": man.CreatedAt,
	})
	return out, nil
}

// CheckpointListTool returns checkpoints for the current session.
type CheckpointListTool struct {
	mgr       *checkpoint.Manager
	sessionFn func() string
}

func NewCheckpointListTool(m *checkpoint.Manager, sessionFn func() string) *CheckpointListTool {
	return &CheckpointListTool{mgr: m, sessionFn: sessionFn}
}

func (t *CheckpointListTool) Name() string { return "checkpoint_list" }

func (t *CheckpointListTool) Execute(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
	if t.mgr == nil {
		return errorJSON("checkpoint manager not configured"), nil
	}
	sid := ""
	if t.sessionFn != nil {
		sid = t.sessionFn()
	}
	list, err := t.mgr.List(sid)
	if err != nil {
		return errorJSON(err.Error()), nil
	}
	out, _ := json.Marshal(map[string]any{"checkpoints": list, "count": len(list)})
	return out, nil
}

// CheckpointRestoreTool rolls back to a named checkpoint.
type CheckpointRestoreTool struct {
	mgr *checkpoint.Manager
}

func NewCheckpointRestoreTool(m *checkpoint.Manager) *CheckpointRestoreTool {
	return &CheckpointRestoreTool{mgr: m}
}

func (t *CheckpointRestoreTool) Name() string { return "checkpoint_restore" }

type checkpointRestoreInput struct {
	ID string `json:"id"`
}

func (t *CheckpointRestoreTool) Execute(ctx context.Context, in json.RawMessage) (json.RawMessage, error) {
	if t.mgr == nil {
		return errorJSON("checkpoint manager not configured"), nil
	}
	var req checkpointRestoreInput
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, fmt.Errorf("checkpoint_restore: %w", err)
	}
	if req.ID == "" {
		return errorJSON("id is required"), nil
	}
	skipped, err := t.mgr.Restore(req.ID)
	if err != nil {
		return errorJSON(err.Error()), nil
	}
	out, _ := json.Marshal(map[string]any{"restored": req.ID, "skipped": skipped})
	return out, nil
}

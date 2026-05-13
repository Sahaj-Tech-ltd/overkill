package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Sahaj-Tech-ltd/overkill/internal/worktree"
)

// WorktreeListTool surfaces existing git worktrees.
type WorktreeListTool struct{ repo string }

func NewWorktreeListTool(repo string) *WorktreeListTool { return &WorktreeListTool{repo: repo} }
func (t *WorktreeListTool) Name() string                { return "worktree_list" }
func (t *WorktreeListTool) Execute(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
	wts, err := worktree.List(t.repo)
	if err != nil {
		return nil, fmt.Errorf("worktree_list: %w", err)
	}
	return json.Marshal(map[string]any{"worktrees": wts})
}

// WorktreeAddTool creates a new isolated workspace.
type WorktreeAddTool struct{ repo string }
type worktreeAddInput struct {
	Path   string `json:"path"`
	Branch string `json:"branch"`
}

func NewWorktreeAddTool(repo string) *WorktreeAddTool { return &WorktreeAddTool{repo: repo} }
func (t *WorktreeAddTool) Name() string               { return "worktree_add" }
func (t *WorktreeAddTool) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	var in worktreeAddInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, err
	}
	if in.Path == "" {
		return nil, fmt.Errorf("worktree_add: path required")
	}
	if err := worktree.Add(t.repo, in.Path, in.Branch); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"ok": true, "path": in.Path, "branch": in.Branch})
}

// WorktreeRemoveTool removes a worktree (risky — agent must route via approval).
type WorktreeRemoveTool struct{ repo string }
type worktreeRemoveInput struct {
	Path  string `json:"path"`
	Force bool   `json:"force"`
}

func NewWorktreeRemoveTool(repo string) *WorktreeRemoveTool { return &WorktreeRemoveTool{repo: repo} }
func (t *WorktreeRemoveTool) Name() string                  { return "worktree_remove" }
func (t *WorktreeRemoveTool) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	var in worktreeRemoveInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, err
	}
	if in.Path == "" {
		return nil, fmt.Errorf("worktree_remove: path required")
	}
	var err error
	if in.Force {
		err = worktree.RemoveForce(t.repo, in.Path)
	} else {
		err = worktree.Remove(t.repo, in.Path)
	}
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"ok": true, "path": in.Path})
}

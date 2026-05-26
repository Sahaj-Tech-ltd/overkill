// Package tools — playbook_* tools surface the ACE-style playbook
// store (§8.2 Phase 5 #6) to the agent.
//
// Typical flow:
//   1. agent calls playbook_rank(task_type="migration") and reads
//      top-K candidates.
//   2. agent calls playbook_use(id=X) to record selection. Bumps
//      LastUsedAt + UseCount.
//   3. agent reads the playbook's Content into its own context and
//      executes the procedure.
//   4. After completion, agent calls playbook_record_outcome(id=X,
//      success=true|false). Counter updates.
//   5. If the playbook would have worked with a tweak, agent calls
//      playbook_refine(parent_id=X, content=...) to create a child
//      version. The parent's history stays intact; the child starts
//      with a neutral prior.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Sahaj-Tech-ltd/overkill/internal/playbooks"
)

// PlaybooksStore is the minimal surface the tools need.
type PlaybooksStore interface {
	Create(pb *playbooks.Playbook) (*playbooks.Playbook, error)
	Get(id string) (*playbooks.Playbook, error)
	Delete(id string) error
	Use(id string) (*playbooks.Playbook, error)
	RecordOutcome(id string, success bool) (*playbooks.Playbook, error)
	Refine(parentID, content, description string) (*playbooks.Playbook, error)
	All() ([]*playbooks.Playbook, error)
	Rank(taskType, query string, topK int, opts playbooks.RankOptions) ([]playbooks.Hit, error)
}

// ── playbook_create ─────────────────────────────────────────────────

type PlaybookCreateTool struct{ store PlaybooksStore }

func NewPlaybookCreateTool(s PlaybooksStore) *PlaybookCreateTool {
	return &PlaybookCreateTool{store: s}
}
func (t *PlaybookCreateTool) Name() string { return "playbook_create" }

type playbookCreateInput struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	TaskTypes   []string `json:"task_types"`
	Content     string   `json:"content"`
	Tags        []string `json:"tags,omitempty"`
}

func (t *PlaybookCreateTool) Execute(_ context.Context, in json.RawMessage) (json.RawMessage, error) {
	if t.store == nil {
		return errorJSON("playbooks store not configured"), nil
	}
	var req playbookCreateInput
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, fmt.Errorf("playbook_create: %w", err)
	}
	pb, err := t.store.Create(&playbooks.Playbook{
		Name:        req.Name,
		Description: req.Description,
		TaskTypes:   req.TaskTypes,
		Content:     req.Content,
		Tags:        req.Tags,
	})
	if err != nil {
		return errorJSON(err.Error()), nil
	}
	return json.Marshal(pb)
}

// ── playbook_rank ───────────────────────────────────────────────────

type PlaybookRankTool struct{ store PlaybooksStore }

func NewPlaybookRankTool(s PlaybooksStore) *PlaybookRankTool { return &PlaybookRankTool{store: s} }
func (t *PlaybookRankTool) Name() string                     { return "playbook_rank" }

type playbookRankInput struct {
	TaskType string `json:"task_type,omitempty"`
	Query    string `json:"query,omitempty"`
	TopK     int    `json:"top_k,omitempty"`
}

func (t *PlaybookRankTool) Execute(_ context.Context, in json.RawMessage) (json.RawMessage, error) {
	if t.store == nil {
		return errorJSON("playbooks store not configured"), nil
	}
	var req playbookRankInput
	if len(in) > 0 {
		_ = json.Unmarshal(in, &req)
	}
	if req.TopK <= 0 {
		req.TopK = 5
	}
	hits, err := t.store.Rank(req.TaskType, req.Query, req.TopK, playbooks.RankOptions{})
	if err != nil {
		return errorJSON(err.Error()), nil
	}
	return json.Marshal(map[string]any{"hits": hits, "count": len(hits)})
}

// ── playbook_use ────────────────────────────────────────────────────

type PlaybookUseTool struct{ store PlaybooksStore }

func NewPlaybookUseTool(s PlaybooksStore) *PlaybookUseTool { return &PlaybookUseTool{store: s} }
func (t *PlaybookUseTool) Name() string                    { return "playbook_use" }

type playbookIDInput struct {
	ID string `json:"id"`
}

func (t *PlaybookUseTool) Execute(_ context.Context, in json.RawMessage) (json.RawMessage, error) {
	if t.store == nil {
		return errorJSON("playbooks store not configured"), nil
	}
	var req playbookIDInput
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, fmt.Errorf("playbook_use: %w", err)
	}
	if req.ID == "" {
		return errorJSON("playbook_use: 'id' is required"), nil
	}
	pb, err := t.store.Use(req.ID)
	if err != nil {
		return errorJSON(err.Error()), nil
	}
	return json.Marshal(pb)
}

// ── playbook_record_outcome ─────────────────────────────────────────

type PlaybookRecordOutcomeTool struct{ store PlaybooksStore }

func NewPlaybookRecordOutcomeTool(s PlaybooksStore) *PlaybookRecordOutcomeTool {
	return &PlaybookRecordOutcomeTool{store: s}
}
func (t *PlaybookRecordOutcomeTool) Name() string { return "playbook_record_outcome" }

type playbookOutcomeInput struct {
	ID      string `json:"id"`
	Success bool   `json:"success"`
}

func (t *PlaybookRecordOutcomeTool) Execute(_ context.Context, in json.RawMessage) (json.RawMessage, error) {
	if t.store == nil {
		return errorJSON("playbooks store not configured"), nil
	}
	var req playbookOutcomeInput
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, fmt.Errorf("playbook_record_outcome: %w", err)
	}
	if req.ID == "" {
		return errorJSON("playbook_record_outcome: 'id' is required"), nil
	}
	pb, err := t.store.RecordOutcome(req.ID, req.Success)
	if err != nil {
		return errorJSON(err.Error()), nil
	}
	return json.Marshal(pb)
}

// ── playbook_refine ─────────────────────────────────────────────────

type PlaybookRefineTool struct{ store PlaybooksStore }

func NewPlaybookRefineTool(s PlaybooksStore) *PlaybookRefineTool {
	return &PlaybookRefineTool{store: s}
}
func (t *PlaybookRefineTool) Name() string { return "playbook_refine" }

type playbookRefineInput struct {
	ParentID    string `json:"parent_id"`
	Content     string `json:"content"`
	Description string `json:"description,omitempty"`
}

func (t *PlaybookRefineTool) Execute(_ context.Context, in json.RawMessage) (json.RawMessage, error) {
	if t.store == nil {
		return errorJSON("playbooks store not configured"), nil
	}
	var req playbookRefineInput
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, fmt.Errorf("playbook_refine: %w", err)
	}
	if req.ParentID == "" || strings.TrimSpace(req.Content) == "" {
		return errorJSON("playbook_refine: 'parent_id' and 'content' are required"), nil
	}
	pb, err := t.store.Refine(req.ParentID, req.Content, req.Description)
	if err != nil {
		return errorJSON(err.Error()), nil
	}
	return json.Marshal(pb)
}

// ── playbook_list ───────────────────────────────────────────────────

type PlaybookListTool struct{ store PlaybooksStore }

func NewPlaybookListTool(s PlaybooksStore) *PlaybookListTool { return &PlaybookListTool{store: s} }
func (t *PlaybookListTool) Name() string                     { return "playbook_list" }

func (t *PlaybookListTool) Execute(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
	if t.store == nil {
		return errorJSON("playbooks store not configured"), nil
	}
	all, err := t.store.All()
	if err != nil {
		return errorJSON(err.Error()), nil
	}
	return json.Marshal(map[string]any{"playbooks": all, "count": len(all)})
}

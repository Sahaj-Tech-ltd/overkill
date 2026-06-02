// Package tools — plan_set / plan_check / plan_status / plan_clear
// expose the per-session plan store (internal/plan) to the agent.
// One active plan per session; the TUI right pane reads from the
// same store so the user sees what the agent committed to and what
// it's already ticked off.
//
// record_learning + learnings_search expose the durable learnings
// stream — the prose reflection layer the agent fills in at
// end-of-task. Stored append-only; read paths support both global
// and model-scoped (§4.16) lookups.
package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Sahaj-Tech-ltd/overkill/internal/plan"
)

// PlanQuerier is the minimal surface the plan tools need. Local
// interface keeps internal/tools free of concrete-store imports
// for testing — same pattern as JournalQuerier.
type PlanQuerier interface {
	Current() *plan.Plan
	Set(title string, items []string) (*plan.Plan, error)
	Check(itemID, note string) (*plan.Plan, error)
	Uncheck(itemID string) (*plan.Plan, error)
	Clear() error
	Remaining() int
}

// ── plan_set ────────────────────────────────────────────────────────

type PlanSetTool struct{ q PlanQuerier }

func NewPlanSetTool(q PlanQuerier) *PlanSetTool { return &PlanSetTool{q: q} }
func (t *PlanSetTool) Name() string             { return "plan_set" }

type planSetInput struct {
	Title string   `json:"title"`
	Items []string `json:"items"`
}

func (t *PlanSetTool) Execute(_ context.Context, in json.RawMessage) (json.RawMessage, error) {
	if t.q == nil {
		return errorJSON("plan store not configured"), nil
	}
	var req planSetInput
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, fmt.Errorf("plan_set: %w", err)
	}
	if len(req.Items) == 0 {
		return errorJSON("plan_set: at least one item is required"), nil
	}
	p, err := t.q.Set(req.Title, req.Items)
	if err != nil {
		return errorJSON(err.Error()), nil
	}
	return json.Marshal(p)
}

// ── plan_check ──────────────────────────────────────────────────────

type PlanCheckTool struct{ q PlanQuerier }

func NewPlanCheckTool(q PlanQuerier) *PlanCheckTool { return &PlanCheckTool{q: q} }
func (t *PlanCheckTool) Name() string               { return "plan_check" }

type planCheckInput struct {
	ItemID string `json:"item_id"`
	Note   string `json:"note,omitempty"`
	Undo   bool   `json:"undo,omitempty"` // unset back to pending
}

func (t *PlanCheckTool) Execute(_ context.Context, in json.RawMessage) (json.RawMessage, error) {
	if t.q == nil {
		return errorJSON("plan store not configured"), nil
	}
	var req planCheckInput
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, fmt.Errorf("plan_check: %w", err)
	}
	if req.ItemID == "" {
		return errorJSON("plan_check: item_id is required"), nil
	}
	var (
		p   *plan.Plan
		err error
	)
	if req.Undo {
		p, err = t.q.Uncheck(req.ItemID)
	} else {
		p, err = t.q.Check(req.ItemID, req.Note)
	}
	if err != nil {
		return errorJSON(err.Error()), nil
	}
	return json.Marshal(p)
}

// ── plan_status ─────────────────────────────────────────────────────

type PlanStatusTool struct{ q PlanQuerier }

func NewPlanStatusTool(q PlanQuerier) *PlanStatusTool { return &PlanStatusTool{q: q} }
func (t *PlanStatusTool) Name() string                { return "plan_status" }

func (t *PlanStatusTool) Execute(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
	if t.q == nil {
		return errorJSON("plan store not configured"), nil
	}
	p := t.q.Current()
	body := map[string]any{
		"plan":      p,
		"remaining": t.q.Remaining(),
	}
	return json.Marshal(body)
}

// ── plan_clear ──────────────────────────────────────────────────────

type PlanClearTool struct{ q PlanQuerier }

func NewPlanClearTool(q PlanQuerier) *PlanClearTool { return &PlanClearTool{q: q} }
func (t *PlanClearTool) Name() string               { return "plan_clear" }

func (t *PlanClearTool) Execute(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
	if t.q == nil {
		return errorJSON("plan store not configured"), nil
	}
	if err := t.q.Clear(); err != nil {
		return errorJSON(err.Error()), nil
	}
	return json.Marshal(map[string]any{"ok": true})
}

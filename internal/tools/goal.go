// Package tools — create_goal / get_goal / update_goal
// expose the per-session GoalStore to the agent. Follows the same
// pattern as plan tools (PlanQuerier interface for testability).
package tools

import (
	"context"
	"encoding/json"
	"fmt"
)

// GoalQuerier is the minimal surface the goal tools need. Defined
// locally so internal/tools stays free of concrete-store imports
// for testing — same pattern as PlanQuerier.
type GoalQuerier interface {
	SetWithBudgets(ctx context.Context, sessionID, text string, tokenBudget, timeBudget int) error
	Get(ctx context.Context, sessionID string) (goalJSON json.RawMessage, err error)
	SetStatus(ctx context.Context, sessionID string, status string) error
	GetStatus(ctx context.Context, sessionID string) (string, error)
}

// SessionGoalQuerier adapts a concrete GoalStore to the querier interface.
// The agent wiring layer creates this and passes it to tool constructors.
type SessionGoalQuerier struct {
	store     GoalStoreBackend
	sessionID func() string
}

// GoalStoreBackend is the concrete store interface the tools need.
type GoalStoreBackend interface {
	SetWithBudgets(ctx context.Context, sessionID, text string, tokenBudget, timeBudget int) error
	Get(ctx context.Context, sessionID string) (json.RawMessage, error)
	SetStatus(ctx context.Context, sessionID string, status string) error
}

// NewSessionGoalQuerier wraps a GoalStoreBackend with a session ID supplier.
func NewSessionGoalQuerier(store GoalStoreBackend, sessionIDFn func() string) *SessionGoalQuerier {
	return &SessionGoalQuerier{store: store, sessionID: sessionIDFn}
}

func (q *SessionGoalQuerier) SetWithBudgets(ctx context.Context, sessionID, text string, tokenBudget, timeBudget int) error {
	return q.store.SetWithBudgets(ctx, sessionID, text, tokenBudget, timeBudget)
}

func (q *SessionGoalQuerier) Get(ctx context.Context, sessionID string) (json.RawMessage, error) {
	return q.store.Get(ctx, sessionID)
}

func (q *SessionGoalQuerier) SetStatus(ctx context.Context, sessionID string, status string) error {
	return q.store.SetStatus(ctx, sessionID, status)
}

func (q *SessionGoalQuerier) GetStatus(ctx context.Context, sessionID string) (string, error) {
	g, err := q.store.Get(ctx, sessionID)
	if err != nil {
		return "", err
	}
	if g == nil {
		return "", nil
	}
	var m map[string]any
	if err := json.Unmarshal(g, &m); err != nil {
		return "", err
	}
	s, _ := m["status"].(string)
	return s, nil
}

// ── create_goal ─────────────────────────────────────────────────────

type CreateGoalTool struct{ q GoalQuerier }

func NewCreateGoalTool(q GoalQuerier) *CreateGoalTool { return &CreateGoalTool{q: q} }
func (t *CreateGoalTool) Name() string                 { return "create_goal" }

type createGoalInput struct {
	Objective   string `json:"objective"`
	TokenBudget int    `json:"token_budget,omitempty"`
}

func (t *CreateGoalTool) Execute(_ context.Context, in json.RawMessage) (json.RawMessage, error) {
	if t.q == nil {
		return errorJSON("goal store not configured"), nil
	}
	var req createGoalInput
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, fmt.Errorf("create_goal: %w", err)
	}
	if req.Objective == "" {
		return errorJSON("create_goal: objective is required"), nil
	}
	// Session ID is "" for tools — the querier should use its sessionID provider.
	if err := t.q.SetWithBudgets(context.Background(), "", req.Objective, req.TokenBudget, 0); err != nil {
		return errorJSON(err.Error()), nil
	}
	g, err := t.q.Get(context.Background(), "")
	if err != nil {
		return errorJSON(err.Error()), nil
	}
	return g, nil
}

// ── get_goal ────────────────────────────────────────────────────────

type GetGoalTool struct{ q GoalQuerier }

func NewGetGoalTool(q GoalQuerier) *GetGoalTool { return &GetGoalTool{q: q} }
func (t *GetGoalTool) Name() string              { return "get_goal" }

func (t *GetGoalTool) Execute(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
	if t.q == nil {
		return errorJSON("goal store not configured"), nil
	}
	g, err := t.q.Get(context.Background(), "")
	if err != nil {
		return errorJSON(err.Error()), nil
	}
	if g == nil {
		return json.Marshal(map[string]any{"goal": nil, "message": "no goal set"})
	}
	return g, nil
}

// ── update_goal ─────────────────────────────────────────────────────

type UpdateGoalTool struct{ q GoalQuerier }

func NewUpdateGoalTool(q GoalQuerier) *UpdateGoalTool { return &UpdateGoalTool{q: q} }
func (t *UpdateGoalTool) Name() string                { return "update_goal" }

type updateGoalInput struct {
	Status string `json:"status"`
}

func (t *UpdateGoalTool) Execute(_ context.Context, in json.RawMessage) (json.RawMessage, error) {
	if t.q == nil {
		return errorJSON("goal store not configured"), nil
	}
	var req updateGoalInput
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, fmt.Errorf("update_goal: %w", err)
	}
	if req.Status != "complete" && req.Status != "blocked" {
		return errorJSON("update_goal: status must be 'complete' or 'blocked'"), nil
	}
	if err := t.q.SetStatus(context.Background(), "", req.Status); err != nil {
		return errorJSON(err.Error()), nil
	}
	g, err := t.q.Get(context.Background(), "")
	if err != nil {
		return errorJSON(err.Error()), nil
	}
	return g, nil
}

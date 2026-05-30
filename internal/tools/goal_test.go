package tools

import (
	"context"
	"encoding/json"
	"testing"
)

// fakeGoalQuerier is a test double for GoalQuerier.
type fakeGoalQuerier struct {
	stored  json.RawMessage
	status  string
	setErr  error
	getErr  error
	statErr error
}

func (f *fakeGoalQuerier) SetWithBudgets(ctx context.Context, sessionID, text string, tokenBudget, timeBudget int) error {
	if f.setErr != nil {
		return f.setErr
	}
	g := map[string]any{
		"text":               text,
		"active":             true,
		"status":             "",
		"token_budget":       tokenBudget,
		"tokens_used":        0,
		"time_budget_seconds": timeBudget,
		"time_used_seconds":  0,
	}
	b, _ := json.Marshal(g)
	f.stored = b
	f.status = ""
	return nil
}

func (f *fakeGoalQuerier) Get(ctx context.Context, sessionID string) (json.RawMessage, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.stored == nil {
		return nil, nil
	}
	// Merge current status into the result.
	var m map[string]any
	if err := json.Unmarshal(f.stored, &m); err != nil {
		return nil, err
	}
	m["status"] = f.status
	return json.Marshal(m)
}

func (f *fakeGoalQuerier) SetStatus(ctx context.Context, sessionID string, status string) error {
	if f.statErr != nil {
		return f.statErr
	}
	f.status = status
	return nil
}

func (f *fakeGoalQuerier) GetStatus(ctx context.Context, sessionID string) (string, error) {
	return f.status, nil
}

func newFakeGoalQuerier() *fakeGoalQuerier {
	return &fakeGoalQuerier{}
}

// ── create_goal tests ───────────────────────────────────────────────

func TestCreateGoalTool_HappyPath(t *testing.T) {
	q := newFakeGoalQuerier()
	tool := NewCreateGoalTool(q)

	in := json.RawMessage(`{"objective": "build the MVP", "token_budget": 5000}`)
	out, err := tool.Execute(context.Background(), in)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result["text"] != "build the MVP" {
		t.Fatalf("got text %q, want 'build the MVP'", result["text"])
	}
	if tb, ok := result["token_budget"].(float64); !ok || int(tb) != 5000 {
		t.Fatalf("got token_budget %v, want 5000", result["token_budget"])
	}
}

func TestCreateGoalTool_MissingObjective(t *testing.T) {
	q := newFakeGoalQuerier()
	tool := NewCreateGoalTool(q)

	out, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var result map[string]any
	json.Unmarshal(out, &result)
	if result["error"] == nil || result["error"].(string) == "" {
		t.Fatal("expected error for missing objective")
	}
}

func TestCreateGoalTool_NilQuerier(t *testing.T) {
	tool := NewCreateGoalTool(nil)
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"objective": "test"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var result map[string]any
	json.Unmarshal(out, &result)
	if result["error"] == nil {
		t.Fatal("expected error for nil querier")
	}
}

// ── get_goal tests ──────────────────────────────────────────────────

func TestGetGoalTool_HappyPath(t *testing.T) {
	q := newFakeGoalQuerier()
	// Pre-populate via create_goal path.
	q.SetWithBudgets(context.Background(), "", "test goal", 1000, 0)

	tool := NewGetGoalTool(q)
	out, err := tool.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result["text"] != "test goal" {
		t.Fatalf("got text %q, want 'test goal'", result["text"])
	}
}

func TestGetGoalTool_NoGoal(t *testing.T) {
	q := newFakeGoalQuerier()
	tool := NewGetGoalTool(q)

	out, err := tool.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var result map[string]any
	json.Unmarshal(out, &result)
	if result["goal"] != nil {
		t.Fatal("expected goal=nil for no goal")
	}
}

func TestGetGoalTool_NilQuerier(t *testing.T) {
	tool := NewGetGoalTool(nil)
	out, err := tool.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var result map[string]any
	json.Unmarshal(out, &result)
	if result["error"] == nil {
		t.Fatal("expected error for nil querier")
	}
}

// ── update_goal tests ───────────────────────────────────────────────

func TestUpdateGoalTool_Complete(t *testing.T) {
	q := newFakeGoalQuerier()
	q.SetWithBudgets(context.Background(), "", "task", 0, 0)

	tool := NewUpdateGoalTool(q)
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"status": "complete"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result["status"] != "complete" {
		t.Fatalf("got status %q, want 'complete'", result["status"])
	}
}

func TestUpdateGoalTool_Blocked(t *testing.T) {
	q := newFakeGoalQuerier()
	q.SetWithBudgets(context.Background(), "", "task", 0, 0)

	tool := NewUpdateGoalTool(q)
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"status": "blocked"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result["status"] != "blocked" {
		t.Fatalf("got status %q, want 'blocked'", result["status"])
	}
}

func TestUpdateGoalTool_InvalidStatus(t *testing.T) {
	q := newFakeGoalQuerier()
	q.SetWithBudgets(context.Background(), "", "task", 0, 0)

	tool := NewUpdateGoalTool(q)
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"status": "in_progress"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var result map[string]any
	json.Unmarshal(out, &result)
	if result["error"] == nil {
		t.Fatal("expected error for invalid status")
	}
}

func TestUpdateGoalTool_NilQuerier(t *testing.T) {
	tool := NewUpdateGoalTool(nil)
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"status": "complete"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var result map[string]any
	json.Unmarshal(out, &result)
	if result["error"] == nil {
		t.Fatal("expected error for nil querier")
	}
}

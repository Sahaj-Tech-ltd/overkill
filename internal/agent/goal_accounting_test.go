package agent

import (
	"context"
	"testing"
)

func TestGoalAccounting_BudgetExceeded(t *testing.T) {
	db := openGoalDB(t)
	gs, err := NewGoalStore(db)
	if err != nil {
		t.Fatalf("NewGoalStore: %v", err)
	}
	ctx := context.Background()
	sid := "test-acct-budget"

	// Create goal with a 100-token budget.
	if err := gs.SetWithBudgets(ctx, sid, "ship it", 100, 0); err != nil {
		t.Fatalf("SetWithBudgets: %v", err)
	}

	acct := NewGoalAccountingState(gs, sid)

	// Start: 0 tokens used.
	acct.RecordTurnStart(ctx)

	// End: 150 tokens used — exceeds 100 budget.
	acct.RecordTurnEnd(ctx, 150)

	// Verify status was set to budget_limited.
	g, err := gs.Get(ctx, sid)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if g.Status != "budget_limited" {
		t.Fatalf("got status %q, want 'budget_limited'", g.Status)
	}
}

func TestGoalAccounting_BudgetNotExceeded(t *testing.T) {
	db := openGoalDB(t)
	gs, err := NewGoalStore(db)
	if err != nil {
		t.Fatalf("NewGoalStore: %v", err)
	}
	ctx := context.Background()
	sid := "test-acct-safe"

	// Create goal with a 1000-token budget.
	if err := gs.SetWithBudgets(ctx, sid, "ship it", 1000, 0); err != nil {
		t.Fatalf("SetWithBudgets: %v", err)
	}

	acct := NewGoalAccountingState(gs, sid)
	acct.RecordTurnStart(ctx)

	// End: 500 tokens used — within budget.
	acct.RecordTurnEnd(ctx, 500)

	g, err := gs.Get(ctx, sid)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if g.Status != "" {
		t.Fatalf("got status %q, want '' (no budget exceeded)", g.Status)
	}
}

func TestGoalAccounting_NilStore(t *testing.T) {
	// Should not panic.
	acct := NewGoalAccountingState(nil, "any")
	acct.RecordTurnStart(context.Background())
	acct.RecordTurnEnd(context.Background(), 100)
}

func TestGoalAccounting_NoBudget(t *testing.T) {
	db := openGoalDB(t)
	gs, err := NewGoalStore(db)
	if err != nil {
		t.Fatalf("NewGoalStore: %v", err)
	}
	ctx := context.Background()
	sid := "test-acct-nobudget"

	// Create goal with no budget (0 = unlimited).
	if err := gs.Set(ctx, sid, "no budget"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	acct := NewGoalAccountingState(gs, sid)
	acct.RecordTurnStart(ctx)
	acct.RecordTurnEnd(ctx, 99999)

	g, err := gs.Get(ctx, sid)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if g.Status != "" {
		t.Fatalf("got status %q, want '' (no budget to exceed)", g.Status)
	}
}

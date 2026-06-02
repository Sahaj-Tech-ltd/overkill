package agent

import (
	"context"
	"database/sql"
	"os"
	"testing"

	_ "github.com/lib/pq"
)

func openGoalDB(t *testing.T) *sql.DB {
	t.Helper()
	connStr := os.Getenv("DATABASE_URL")
	if connStr == "" {
		connStr = os.Getenv("PG_TEST_URL")
	}
	if connStr == "" {
		connStr = "postgres://postgres:postgres@localhost:5432/overkill_test?sslmode=disable"
	}
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		t.Skipf("skipping: cannot open postgres: %v", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		t.Skipf("skipping: cannot ping postgres: %v (set PG_TEST_URL or DATABASE_URL)", err)
	}
	// Create goals table if it doesn't exist.
	_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS goals (
		session_id TEXT PRIMARY KEY,
		text TEXT NOT NULL DEFAULT '',
		active BOOLEAN NOT NULL DEFAULT true,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		token_budget INTEGER NOT NULL DEFAULT 0,
		tokens_used INTEGER NOT NULL DEFAULT 0,
		time_budget_seconds INTEGER NOT NULL DEFAULT 0,
		time_used_seconds INTEGER NOT NULL DEFAULT 0,
		status TEXT NOT NULL DEFAULT ''
	)`)
	// Add missing columns for existing tables.
	for _, col := range []string{
		`ALTER TABLE goals ADD COLUMN IF NOT EXISTS token_budget INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE goals ADD COLUMN IF NOT EXISTS tokens_used INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE goals ADD COLUMN IF NOT EXISTS time_budget_seconds INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE goals ADD COLUMN IF NOT EXISTS time_used_seconds INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE goals ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT ''`,
	} {
		_, _ = db.Exec(col)
	}
	t.Cleanup(func() {
		db.Exec("DELETE FROM goals WHERE session_id LIKE 'test-%'")
		db.Close()
	})
	return db
}

func TestGoal_SetAndGet(t *testing.T) {
	gs, err := NewGoalStore(openGoalDB(t))
	if err != nil {
		t.Fatalf("NewGoalStore: %v", err)
	}
	ctx := context.Background()
	sid := "test-session"

	if err := gs.Set(ctx, sid, "ship the MVP"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	g, err := gs.Get(ctx, sid)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if g == nil {
		t.Fatal("goal is nil")
	}
	if g.Text != "ship the MVP" {
		t.Fatalf("got text %q, want 'ship the MVP'", g.Text)
	}
	if !g.Active {
		t.Fatal("expected active=true")
	}
	if g.CreatedAt.IsZero() || g.UpdatedAt.IsZero() {
		t.Fatal("timestamps not set")
	}
}

func TestGoal_Pause(t *testing.T) {
	gs, err := NewGoalStore(openGoalDB(t))
	if err != nil {
		t.Fatalf("NewGoalStore: %v", err)
	}
	ctx := context.Background()
	sid := "test-pause"
	gs.Set(ctx, sid, "test")
	gs.Pause(ctx, sid)

	g, err := gs.Get(ctx, sid)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if g.Active {
		t.Fatal("expected active=false after pause")
	}
}

func TestGoal_Resume(t *testing.T) {
	gs, err := NewGoalStore(openGoalDB(t))
	if err != nil {
		t.Fatalf("NewGoalStore: %v", err)
	}
	ctx := context.Background()
	sid := "test-resume"
	gs.Set(ctx, sid, "test")
	gs.Pause(ctx, sid)
	gs.Resume(ctx, sid)

	g, err := gs.Get(ctx, sid)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !g.Active {
		t.Fatal("expected active=true after resume")
	}
}

func TestGoal_Clear(t *testing.T) {
	gs, err := NewGoalStore(openGoalDB(t))
	if err != nil {
		t.Fatalf("NewGoalStore: %v", err)
	}
	ctx := context.Background()
	sid := "test-clear"
	gs.Set(ctx, sid, "test")
	gs.Clear(ctx, sid)

	g, err := gs.Get(ctx, sid)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if g != nil {
		t.Fatal("expected nil goal after clear")
	}
}

func TestGoal_GetMissing(t *testing.T) {
	gs, err := NewGoalStore(openGoalDB(t))
	if err != nil {
		t.Fatalf("NewGoalStore: %v", err)
	}
	ctx := context.Background()
	g, err := gs.Get(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if g != nil {
		t.Fatal("expected nil for missing goal")
	}
}

func TestGoal_SetWithBudgets(t *testing.T) {
	gs, err := NewGoalStore(openGoalDB(t))
	if err != nil {
		t.Fatalf("NewGoalStore: %v", err)
	}
	ctx := context.Background()
	sid := "test-budgets"

	if err := gs.SetWithBudgets(ctx, sid, "build feature", 10000, 3600); err != nil {
		t.Fatalf("SetWithBudgets: %v", err)
	}

	g, err := gs.Get(ctx, sid)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if g == nil {
		t.Fatal("goal is nil")
	}
	if g.TokenBudget != 10000 {
		t.Fatalf("got token_budget %d, want 10000", g.TokenBudget)
	}
	if g.TimeBudgetSeconds != 3600 {
		t.Fatalf("got time_budget_seconds %d, want 3600", g.TimeBudgetSeconds)
	}
}

func TestGoal_SetStatus(t *testing.T) {
	gs, err := NewGoalStore(openGoalDB(t))
	if err != nil {
		t.Fatalf("NewGoalStore: %v", err)
	}
	ctx := context.Background()
	sid := "test-setstatus"

	gs.Set(ctx, sid, "check status")
	if err := gs.SetStatus(ctx, sid, "budget_limited"); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}

	g, err := gs.Get(ctx, sid)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if g.Status != "budget_limited" {
		t.Fatalf("got status %q, want 'budget_limited'", g.Status)
	}
}

func TestGoal_SetStatus_NoGoal(t *testing.T) {
	gs, err := NewGoalStore(openGoalDB(t))
	if err != nil {
		t.Fatalf("NewGoalStore: %v", err)
	}
	ctx := context.Background()
	err = gs.SetStatus(ctx, "nonexistent", "complete")
	if err == nil {
		t.Fatal("expected error for missing goal")
	}
}

func TestGoal_UpdateUsage(t *testing.T) {
	gs, err := NewGoalStore(openGoalDB(t))
	if err != nil {
		t.Fatalf("NewGoalStore: %v", err)
	}
	ctx := context.Background()
	sid := "test-updateusage"

	gs.Set(ctx, sid, "track usage")
	newTokens, newSeconds, _, err := gs.UpdateUsageAtomic(ctx, sid, 500, 30)
	if err != nil {
		t.Fatalf("UpdateUsageAtomic: %v", err)
	}

	g, err := gs.Get(ctx, sid)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if g.TokensUsed != 500 {
		t.Fatalf("got tokens_used %d, want 500", g.TokensUsed)
	}
	if g.TimeUsedSeconds != 30 {
		t.Fatalf("got time_used_seconds %d, want 30", g.TimeUsedSeconds)
	}
	if newTokens != 500 {
		t.Fatalf("UpdateUsageAtomic returned newTokens %d, want 500", newTokens)
	}
	if newSeconds != 30 {
		t.Fatalf("UpdateUsageAtomic returned newSeconds %d, want 30", newSeconds)
	}
}

func TestGoal_GetJSON(t *testing.T) {
	gs, err := NewGoalStore(openGoalDB(t))
	if err != nil {
		t.Fatalf("NewGoalStore: %v", err)
	}
	ctx := context.Background()
	sid := "test-getjson"

	gs.Set(ctx, sid, "json goal")
	raw, err := gs.GetJSON(ctx, sid)
	if err != nil {
		t.Fatalf("GetJSON: %v", err)
	}
	if raw == nil {
		t.Fatal("GetJSON returned nil")
	}
	if string(raw) == "" {
		t.Fatal("GetJSON returned empty")
	}
}

func TestGoal_GetJSON_Missing(t *testing.T) {
	gs, err := NewGoalStore(openGoalDB(t))
	if err != nil {
		t.Fatalf("NewGoalStore: %v", err)
	}
	ctx := context.Background()
	raw, err := gs.GetJSON(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("GetJSON: %v", err)
	}
	if raw != nil {
		t.Fatal("expected nil for missing goal")
	}
}

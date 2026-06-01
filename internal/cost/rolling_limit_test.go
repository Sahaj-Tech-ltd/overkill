package cost

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"

	_ "github.com/lib/pq"
)

func openCostDB(t *testing.T) *sql.DB {
	t.Helper()
	connStr := os.Getenv("DATABASE_URL")
	if connStr == "" {
		connStr = os.Getenv("PG_TEST_URL")
	}
	if connStr == "" {
		connStr = "postgres://postgres:***@localhost:5432/overkill_test?sslmode=disable"
	}
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		t.Skipf("skipping: cannot open postgres: %v", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		t.Skipf("skipping: cannot ping postgres: %v (set PG_TEST_URL or DATABASE_URL)", err)
	}
	_, _ = db.Exec(`CREATE TABLE IF NOT EXISTS cost_entries (
		id SERIAL PRIMARY KEY, session_id TEXT, model TEXT, provider TEXT,
		tokens_in INTEGER, tokens_out INTEGER, cost_usd REAL,
		created_at TIMESTAMPTZ DEFAULT NOW()
	)`)
	t.Cleanup(func() {
		db.Exec("DELETE FROM cost_entries WHERE session_id LIKE 'test-%'")
		db.Close()
	})
	return db
}

func newTrackerWithRollingLimit(t *testing.T, limit float64) *PostgresTracker {
	t.Helper()
	cfg := config.CostConfig{
		RollingWindowHrs: 5,
		RollingLimitUSD:  limit,
		WarnAtPercent:    80,
	}
	db := openCostDB(t)
	tracker, err := NewPostgresTracker(db, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tracker.Close() })
	return tracker
}

func TestRollingLimit_ZeroIsDisabled(t *testing.T) {
	tr := newTrackerWithRollingLimit(t, 0)
	defer tr.Close()
	if err := tr.Record(context.Background(), Entry{
		ID:        "e1",
		SessionID: "test-s1",
		Timestamp: time.Now(),
		CostUSD:   999.0,
	}); err != nil {
		t.Fatal(err)
	}
	status, err := tr.CheckBudget(context.Background(), "test-s1")
	if err != nil {
		t.Fatal(err)
	}
	if status.ShouldAbort || status.ShouldWarn {
		t.Errorf("limit=0 should never trip: %+v", status)
	}
}

func TestRollingLimit_WarnAt80Percent(t *testing.T) {
	tr := newTrackerWithRollingLimit(t, 10.0)
	defer tr.Close()
	if err := tr.Record(context.Background(), Entry{
		ID:        "e1",
		SessionID: "test-s1",
		Timestamp: time.Now(),
		CostUSD:   8.5,
	}); err != nil {
		t.Fatal(err)
	}
	status, _ := tr.CheckBudget(context.Background(), "test-s1")
	if !status.ShouldWarn {
		t.Errorf("8.5/10 over 80%% should warn: %+v", status)
	}
	if status.ShouldAbort {
		t.Errorf("8.5/10 should NOT abort yet: %+v", status)
	}
}

func TestRollingLimit_AbortAtLimit(t *testing.T) {
	tr := newTrackerWithRollingLimit(t, 10.0)
	defer tr.Close()
	if err := tr.Record(context.Background(), Entry{
		ID:        "e1",
		SessionID: "test-s1",
		Timestamp: time.Now(),
		CostUSD:   10.5,
	}); err != nil {
		t.Fatal(err)
	}
	status, _ := tr.CheckBudget(context.Background(), "test-s1")
	if !status.ShouldAbort {
		t.Errorf("10.5/10 should abort: %+v", status)
	}
}

func TestRollingLimit_OldEntriesDropOutOfWindow(t *testing.T) {
	tr := newTrackerWithRollingLimit(t, 10.0)
	defer tr.Close()
	if err := tr.Record(context.Background(), Entry{
		ID:        "old",
		SessionID: "test-s1",
		Timestamp: time.Now().Add(-6 * time.Hour),
		CostUSD:   100.0,
	}); err != nil {
		t.Fatal(err)
	}
	if err := tr.Record(context.Background(), Entry{
		ID:        "new",
		SessionID: "test-s1",
		Timestamp: time.Now(),
		CostUSD:   1.0,
	}); err != nil {
		t.Fatal(err)
	}
	status, _ := tr.CheckBudget(context.Background(), "test-s1")
	if status.ShouldWarn || status.ShouldAbort {
		t.Errorf("old entry should age out of window: %+v", status)
	}
}

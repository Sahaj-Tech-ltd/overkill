package cost

import (
	"context"
	"database/sql"
	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
)

func openCostDB(t *testing.T) *sql.DB {
	t.Helper()
	database, _ := testpg.Open(t)
	return database
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

package db_test

import (
	"context"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/automation"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/cost"
	"github.com/Sahaj-Tech-ltd/overkill/internal/db"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/stretchr/testify/require"
)

func TestBootstrapAndStoreSchemasAgree(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "fresh"
		if legacy {
			name = "legacy"
		}
		t.Run(name, func(t *testing.T) {
			database, _ := testpg.Open(t)
			ctx := context.Background()
			if legacy {
				_, err := database.ExecContext(ctx, `
					CREATE TABLE suspensions (id TEXT PRIMARY KEY, session_id TEXT NOT NULL,
						state JSONB NOT NULL DEFAULT '{}', suspended_at TIMESTAMPTZ NOT NULL DEFAULT NOW());
					CREATE TABLE automation_routines (id TEXT PRIMARY KEY, name TEXT NOT NULL DEFAULT '',
						steps JSONB NOT NULL DEFAULT '[]', enabled BOOLEAN NOT NULL DEFAULT true,
						created_at TIMESTAMPTZ NOT NULL DEFAULT NOW());
					INSERT INTO automation_routines (id, steps) VALUES ('legacy', '["preserved"]')`)
				require.NoError(t, err)
			}
			require.NoError(t, db.Migrate(database))
			require.NoError(t, db.Migrate(database))
			store, err := session.NewPostgresStore(database)
			require.NoError(t, err)
			sess := session.NewSession(t.TempDir())
			require.NoError(t, store.Create(ctx, sess))
			require.NoError(t, store.Save(ctx, sess))
			tracker, err := cost.NewPostgresTracker(database, config.CostConfig{})
			require.NoError(t, err)
			require.NoError(t, tracker.Record(ctx, cost.Entry{ID: "usage", SessionID: sess.ID, CostUSD: 1}))
			goals, err := agent.NewGoalStore(database)
			require.NoError(t, err)
			require.NoError(t, goals.SetWithBudgets(ctx, sess.ID, "persist", 100, 60))
			routines, err := automation.NewPostgresRoutineStore(database)
			require.NoError(t, err)
			require.NoError(t, routines.Save(&automation.Routine{ID: "new", Name: "new", Trigger: "test", Action: "test", Enabled: true}))
			rows, err := routines.Load()
			require.NoError(t, err)
			require.NotEmpty(t, rows)
			if legacy {
				var preserved string
				require.NoError(t, database.QueryRowContext(ctx, `SELECT steps::text FROM automation_routines WHERE id='legacy'`).Scan(&preserved))
				require.JSONEq(t, `["preserved"]`, preserved)
			}
			requested := make(chan string, 1)
			approver, err := agent.NewSuspendedApprover(database, nil, sess.ID, func(event string, data map[string]any) {
				if event == "needs_approval" {
					requested <- data["call_id"].(string)
				}
			})
			require.NoError(t, err)
			defer approver.Close()
			result := make(chan agent.Approval, 1)
			go func() { result <- approver.Approve("fs", "{}", "low") }()
			select {
			case id := <-requested:
				require.NoError(t, approver.ResumeApproval(id, true, "test"))
			case <-time.After(3 * time.Second):
				t.Fatal("approval was not persisted/emitted after bootstrap")
			}
			select {
			case decision := <-result:
				require.True(t, decision.Allow)
			case <-time.After(3 * time.Second):
				t.Fatal("approval did not resume")
			}
		})
	}
}

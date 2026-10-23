package agent

import (
	"context"
	"database/sql"
	"sync"
	"testing"
	"time"

	appdb "github.com/Sahaj-Tech-ltd/overkill/internal/db"
	"github.com/Sahaj-Tech-ltd/overkill/internal/dbschema"
	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/stretchr/testify/require"
)

func TestGoalMigrationBackfillFailureRollsBackEarlierColumns(t *testing.T) {
	db, _ := testpg.Open(t)
	_, err := db.Exec(`CREATE TABLE goals(session_id TEXT PRIMARY KEY,text TEXT NOT NULL DEFAULT '',active BOOLEAN NOT NULL DEFAULT true,created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),time_used_seconds TEXT NOT NULL DEFAULT 'bad'); INSERT INTO goals(session_id,text) VALUES('saved','preserve goal')`)
	require.NoError(t, err)
	store, err := NewGoalStore(db)
	require.Error(t, err)
	require.Nil(t, store)
	var exists bool
	require.NoError(t, db.QueryRow(`SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='goals' AND column_name='goal_id')`).Scan(&exists))
	require.False(t, exists, "late backfill failure must roll back added columns")
	var prior string
	require.NoError(t, db.QueryRow(`SELECT time_used_seconds FROM goals WHERE session_id='saved'`).Scan(&prior))
	require.Equal(t, "bad", prior)
	_, err = db.Exec(`UPDATE goals SET time_used_seconds='2'; ALTER TABLE goals ALTER COLUMN time_used_seconds DROP DEFAULT; ALTER TABLE goals ALTER COLUMN time_used_seconds TYPE INTEGER USING time_used_seconds::integer; ALTER TABLE goals ALTER COLUMN time_used_seconds SET DEFAULT 0`)
	require.NoError(t, err)
	for range 2 {
		store, err = NewGoalStore(db)
		require.NoError(t, err)
	}
	goal, err := store.Get(t.Context(), "saved")
	require.NoError(t, err)
	require.Equal(t, "preserve goal", goal.Text)
	require.Equal(t, int64(2000000000), goal.TimeUsedNanoseconds)
	require.NotEmpty(t, goal.ID)
	require.NoError(t, store.SetWithBudgets(t.Context(), "saved", "preserve goal", 500, 60))
	updated, err := store.Get(t.Context(), "saved")
	require.NoError(t, err)
	require.Equal(t, goal.ID, updated.ID)
	require.Equal(t, 500, updated.TokenBudget)
	require.Equal(t, int64(2000000000), updated.TimeUsedNanoseconds)
}

func TestGoalMigrationSerializesWithCentralBootstrap(t *testing.T) {
	db, _ := testpg.Open(t)
	start := make(chan struct{})
	errs := make(chan error, 24)
	var wg sync.WaitGroup
	for i := range 24 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if i%2 == 0 {
				errs <- appdb.Migrate(db)
			} else {
				_, err := NewGoalStore(db)
				errs <- err
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	store, err := NewGoalStore(db)
	require.NoError(t, err)
	require.NoError(t, store.SetWithBudgets(t.Context(), "saved", "goal after contention", 1000, 60))
	goal, err := store.Get(t.Context(), "saved")
	require.NoError(t, err)
	require.NotEmpty(t, goal.ID)
	store, err = NewGoalStore(db)
	require.NoError(t, err)
	after, err := store.Get(t.Context(), "saved")
	require.NoError(t, err)
	require.Equal(t, goal.ID, after.ID)
	var table sql.NullString
	require.NoError(t, db.QueryRow(`SELECT to_regclass(current_schema() || '.goal_turn_usage')::text`).Scan(&table))
	require.True(t, table.Valid)
}

func TestGoalMigrationMissingPoolAndLockDeadline(t *testing.T) {
	store, err := NewGoalStore(nil)
	require.Error(t, err)
	require.Nil(t, store)
	db, _ := testpg.Open(t)
	tx, err := db.BeginTx(t.Context(), nil)
	require.NoError(t, err)
	defer tx.Rollback()
	_, err = tx.ExecContext(t.Context(), `SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || ':overkill:goals',0))`)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 80*time.Millisecond)
	defer cancel()
	started := time.Now()
	require.Error(t, dbschema.Goals(ctx, db))
	require.ErrorIs(t, ctx.Err(), context.DeadlineExceeded)
	require.Less(t, time.Since(started), 2*time.Second)
	var table sql.NullString
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT to_regclass(current_schema() || '.goals')::text`).Scan(&table))
	require.False(t, table.Valid)
	require.NoError(t, tx.Rollback())
	store, err = NewGoalStore(db)
	require.NoError(t, err)
	require.NoError(t, store.Set(t.Context(), "saved", "goal after heldlock"))
}

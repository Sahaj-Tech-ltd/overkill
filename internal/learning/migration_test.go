package learning

import (
	"sync"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/stretchr/testify/require"
)

func TestLearningMigrationConcurrentConstructors(t *testing.T) {
	db, _ := testpg.Open(t)
	start := make(chan struct{})
	failures := make(chan error, 16)
	var workers sync.WaitGroup
	for range 16 {
		workers.Go(func() { <-start; _, err := NewStore(db, 100); failures <- err })
	}
	close(start)
	workers.Wait()
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
	store, err := NewStore(db, 100)
	require.NoError(t, err)
	require.NoError(t, store.Save(NewCorrection("retained context", "wrong", "first")))
	_, err = NewStore(db, 100)
	require.NoError(t, err)
	require.NoError(t, store.Save(NewCorrection("retained context", "wrong", "updated")))
	found, err := store.FindCorrections("retained", 1)
	require.NoError(t, err)
	require.Len(t, found, 1)
	require.Equal(t, "updated", found[0].Correct)
}

func TestLearningMigrationNilDatabase(t *testing.T) {
	store, err := NewStore(nil, 100)
	require.ErrorContains(t, err, "database is required")
	require.Nil(t, store)
}

func TestLearningMigrationLockTimeoutAndRetry(t *testing.T) {
	db, _ := testpg.Open(t)
	tx, err := db.BeginTx(t.Context(), nil)
	require.NoError(t, err)
	defer tx.Rollback()
	_, err = tx.ExecContext(t.Context(), `SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || ':overkill:learning', 0))`)
	require.NoError(t, err)
	started := time.Now()
	store, err := NewStore(db, 100)
	require.ErrorContains(t, err, "lock migration")
	require.Nil(t, store)
	require.Less(t, time.Since(started), 20*time.Second, "startup must have a finite deadline")
	var present bool
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT to_regclass('corrections') IS NOT NULL`).Scan(&present))
	require.False(t, present)
	require.NoError(t, tx.Rollback())
	store, err = NewStore(db, 100)
	require.NoError(t, err)
	require.NoError(t, store.Save(NewCorrection("retry after timeout", "wrong", "correct")))
}

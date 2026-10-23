package cron

import (
	"sync"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/stretchr/testify/require"
)

func TestCronMigrationLateFailureRollsBackAndRetries(t *testing.T) {
	db, _ := testpg.Open(t)
	// An incompatible preexisting log table fails only at the last index.
	_, err := db.ExecContext(t.Context(), `CREATE TABLE cron_run_logs (id BIGSERIAL PRIMARY KEY,job_id TEXT,output TEXT); INSERT INTO cron_run_logs(job_id,output) VALUES ('legacy','keep this output')`)
	require.NoError(t, err)
	store, err := NewPostgresJobStore(db)
	require.Error(t, err)
	require.Nil(t, store)
	var present bool
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT to_regclass('cron_jobs') IS NOT NULL`).Scan(&present))
	require.False(t, present, "earlier table creation must roll back")
	var output string
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT output FROM cron_run_logs WHERE job_id='legacy'`).Scan(&output))
	require.Equal(t, "keep this output", output)
	_, err = db.ExecContext(t.Context(), `ALTER TABLE cron_run_logs RENAME TO incompatible_cron_run_logs`)
	require.NoError(t, err)
	for range 2 {
		store, err = NewPostgresJobStore(db)
		require.NoError(t, err)
	}
	job := &Job{ID: "after-repair", CreatedAt: time.Now()}
	require.NoError(t, store.SaveJob(t.Context(), job))
	require.NoError(t, store.LogRun(t.Context(), job.ID, "done", "", true))
	logs, err := store.GetRunLogs(t.Context(), job.ID, 1)
	require.NoError(t, err)
	require.Len(t, logs, 1)
	require.Equal(t, "done", logs[0].Output)
}

func TestCronMigrationConcurrentConstructors(t *testing.T) {
	db, _ := testpg.Open(t)
	start := make(chan struct{})
	failures := make(chan error, 8)
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() { <-start; _, err := NewPostgresJobStore(db); failures <- err })
	}
	close(start)
	workers.Wait()
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
}

func TestCronMigrationNilDatabase(t *testing.T) {
	store, err := NewPostgresJobStore(nil)
	require.ErrorContains(t, err, "database is required")
	require.Nil(t, store)
}

func TestCronMigrationLockTimeoutAndRetry(t *testing.T) {
	db, _ := testpg.Open(t)
	tx, err := db.BeginTx(t.Context(), nil)
	require.NoError(t, err)
	defer tx.Rollback()
	_, err = tx.ExecContext(t.Context(), `SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || ':overkill:cron', 0))`)
	require.NoError(t, err)
	started := time.Now()
	store, err := NewPostgresJobStore(db)
	require.ErrorContains(t, err, "lock migration")
	require.Nil(t, store)
	require.Less(t, time.Since(started), 20*time.Second, "startup must have a finite deadline")
	var present bool
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT to_regclass('cron_jobs') IS NOT NULL`).Scan(&present))
	require.False(t, present)
	require.NoError(t, tx.Rollback())
	store, err = NewPostgresJobStore(db)
	require.NoError(t, err)
	require.NoError(t, store.SaveJob(t.Context(), &Job{ID: "after-timeout", CreatedAt: time.Now()}))
}

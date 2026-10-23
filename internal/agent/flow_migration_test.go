package agent

import (
	"context"
	"database/sql"
	"sync"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/dbschema"
	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/stretchr/testify/require"
)

func TestFlowMigrationConcurrentStartupPreservesState(t *testing.T) {
	db, _ := testpg.Open(t)
	db.SetMaxIdleConns(24)
	var connections []*sql.Conn
	for range 24 {
		conn, err := db.Conn(t.Context())
		require.NoError(t, err)
		connections = append(connections, conn)
	}
	for _, conn := range connections {
		require.NoError(t, conn.Close())
	}
	start := make(chan struct{})
	errs := make(chan error, 24)
	var wg sync.WaitGroup
	for range 24 {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; _, err := NewPostgresFlowStore(db); errs <- err }()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	store, err := NewPostgresFlowStore(db)
	require.NoError(t, err)
	state := &FlowState{ID: "saved", SessionID: "session", UserInput: "preserve this flow"}
	require.NoError(t, store.Save(state))
	for range 2 {
		store, err = NewPostgresFlowStore(db)
		require.NoError(t, err)
	}
	after, err := store.Load("saved")
	require.NoError(t, err)
	require.Equal(t, "preserve this flow", after.UserInput)
	require.Equal(t, "session", after.SessionID)
}

func TestFlowMigrationMissingPoolAndLockDeadline(t *testing.T) {
	store, err := NewPostgresFlowStore(nil)
	require.Error(t, err)
	require.Nil(t, store)
	db, _ := testpg.Open(t)
	tx, err := db.BeginTx(t.Context(), nil)
	require.NoError(t, err)
	defer tx.Rollback()
	_, err = tx.ExecContext(t.Context(), `SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || ':overkill:flow_states',0))`)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 80*time.Millisecond)
	defer cancel()
	started := time.Now()
	require.Error(t, dbschema.FlowStates(ctx, db))
	require.ErrorIs(t, ctx.Err(), context.DeadlineExceeded)
	require.Less(t, time.Since(started), 2*time.Second)
	var table sql.NullString
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT to_regclass(current_schema() || '.flow_states')::text`).Scan(&table))
	require.False(t, table.Valid)
	require.NoError(t, tx.Rollback())
	store, err = NewPostgresFlowStore(db)
	require.NoError(t, err)
	require.NoError(t, store.Save(&FlowState{ID: "saved", UserInput: "after deadline"}))
}

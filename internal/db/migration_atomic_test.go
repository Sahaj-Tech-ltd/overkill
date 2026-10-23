package db_test

import (
	"context"
	"database/sql"
	"github.com/Sahaj-Tech-ltd/overkill/internal/dbschema"
	"sync"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/db"
	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/stretchr/testify/require"
)

func TestBootstrapLateFailureRollsBackAllOwnedSchema(t *testing.T) {
	database, _ := testpg.Open(t)
	_, err := database.ExecContext(t.Context(), `CREATE TABLE suspensions(id TEXT PRIMARY KEY, session_id TEXT NOT NULL, state JSONB NOT NULL DEFAULT '{}', suspended_at TIMESTAMPTZ NOT NULL DEFAULT NOW());
 CREATE TABLE automation_routines(id TEXT PRIMARY KEY, steps JSONB NOT NULL DEFAULT '[]');
 INSERT INTO suspensions(id,session_id,state) VALUES('approval','session','{"keep":true}');
 INSERT INTO automation_routines(id,steps) VALUES('routine','["keep"]');
 CREATE TABLE bookmarks(id SERIAL PRIMARY KEY, session_id TEXT, label JSONB);
 INSERT INTO bookmarks(session_id,label) VALUES('session','{"keep":true}');`)
	require.NoError(t, err)
	// jsonb supports btree, so use json for a genuinely unsupported final index.
	_, err = database.ExecContext(t.Context(), `ALTER TABLE bookmarks ALTER COLUMN label TYPE JSON USING label::json`)
	require.NoError(t, err)
	err = db.Migrate(database)
	require.Error(t, err)
	var columns int
	require.NoError(t, database.QueryRowContext(t.Context(), `SELECT count(*) FROM information_schema.columns WHERE table_schema=current_schema() AND ((table_name='suspensions' AND column_name='tool_name') OR (table_name='automation_routines' AND column_name='trigger'))`).Scan(&columns))
	require.Zero(t, columns, "a failed central bootstrap must not publish earlier shared-store columns")
	var flow sql.NullString
	require.NoError(t, database.QueryRowContext(t.Context(), `SELECT to_regclass('agent_flows')::text`).Scan(&flow))
	require.False(t, flow.Valid)
	var state, steps, label string
	require.NoError(t, database.QueryRowContext(t.Context(), `SELECT state::text FROM suspensions WHERE id='approval'`).Scan(&state))
	require.JSONEq(t, `{"keep":true}`, state)
	require.NoError(t, database.QueryRowContext(t.Context(), `SELECT steps::text FROM automation_routines WHERE id='routine'`).Scan(&steps))
	require.JSONEq(t, `["keep"]`, steps)
	require.NoError(t, database.QueryRowContext(t.Context(), `SELECT label::text FROM bookmarks WHERE session_id='session'`).Scan(&label))
	require.JSONEq(t, `{"keep":true}`, label)
	// Operator chooses the repair; bootstrap must not coerce stored data itself.
	_, err = database.ExecContext(t.Context(), `ALTER TABLE bookmarks ALTER COLUMN label TYPE TEXT USING label::text`)
	require.NoError(t, err)
	require.NoError(t, db.Migrate(database))
	require.NoError(t, db.Migrate(database))
	require.NoError(t, database.QueryRowContext(t.Context(), `SELECT label FROM bookmarks WHERE session_id='session'`).Scan(&label))
	require.JSONEq(t, `{"keep":true}`, label)
}

func TestBootstrapConcurrentConstructors(t *testing.T) {
	database, _ := testpg.Open(t)
	const n = 16
	database.SetMaxOpenConns(n + 2)
	connections := make([]*sql.Conn, 0, n)
	for range n {
		c, err := database.Conn(context.Background())
		require.NoError(t, err)
		connections = append(connections, c)
	}
	for _, c := range connections {
		require.NoError(t, c.Close())
	}
	start := make(chan struct{})
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; errs <- db.Migrate(database) }()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	_, err := database.ExecContext(t.Context(), `INSERT INTO input_history(chat_key,messages) VALUES('chat','["preserved"]')`)
	require.NoError(t, err)
	require.NoError(t, db.Migrate(database))
	var messages string
	require.NoError(t, database.QueryRowContext(t.Context(), `SELECT messages::text FROM input_history WHERE chat_key='chat'`).Scan(&messages))
	require.JSONEq(t, `["preserved"]`, messages)
}

func TestBootstrapLockDeadlineAndRetry(t *testing.T) {
	database, _ := testpg.Open(t)
	_, err := database.ExecContext(t.Context(), `CREATE TABLE suspensions(id TEXT PRIMARY KEY, session_id TEXT NOT NULL)`)
	require.NoError(t, err)
	held, err := database.BeginTx(t.Context(), nil)
	require.NoError(t, err)
	defer held.Rollback()
	_, err = held.ExecContext(t.Context(), `SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || ':overkill:automation_routines',0))`)
	require.NoError(t, err)
	started := time.Now()
	err = db.Migrate(database)
	elapsed := time.Since(started)
	require.ErrorContains(t, err, "lock migration")
	require.GreaterOrEqual(t, elapsed, 8*time.Second)
	require.Less(t, elapsed, 20*time.Second)
	var count int
	require.NoError(t, database.QueryRowContext(t.Context(), `SELECT count(*) FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='suspensions' AND column_name='tool_name'`).Scan(&count))
	require.Zero(t, count)
	require.NoError(t, held.Rollback())
	require.NoError(t, db.Migrate(database))
}

func TestBootstrapSharesStandaloneStoreLocks(t *testing.T) {
	database, _ := testpg.Open(t)
	constructors := []func() error{
		func() error { return db.Migrate(database) },
		func() error { return dbschema.Suspensions(t.Context(), database) },
		func() error { return dbschema.Routines(t.Context(), database) },
	}
	const n = 18
	database.SetMaxOpenConns(n + 2)
	conns := make([]*sql.Conn, 0, n)
	for range n {
		c, err := database.Conn(t.Context())
		require.NoError(t, err)
		conns = append(conns, c)
	}
	for _, c := range conns {
		require.NoError(t, c.Close())
	}
	start := make(chan struct{})
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; errs <- constructors[i%len(constructors)]() }()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.NoError(t, db.Migrate(database))
}

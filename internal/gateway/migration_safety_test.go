package gateway

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

func TestBookmarkMigrationFailureRollsBackEarlierIndex(t *testing.T) {
	db, _ := testpg.Open(t)
	_, err := db.Exec(`CREATE TABLE bookmarks(id SERIAL PRIMARY KEY,session_id TEXT NOT NULL,label JSON NOT NULL,created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()); INSERT INTO bookmarks(session_id,label) VALUES('saved','{"label":"retain"}')`)
	require.NoError(t, err)
	store, err := NewBookmarkStore(db)
	require.Error(t, err)
	require.Nil(t, store)
	var index sql.NullString
	require.NoError(t, db.QueryRow(`SELECT to_regclass(current_schema() || '.idx_bookmarks_session')::text`).Scan(&index))
	require.False(t, index.Valid, "late index failure must roll back earlier DDL")
	var saved string
	require.NoError(t, db.QueryRow(`SELECT label::text FROM bookmarks WHERE session_id='saved'`).Scan(&saved))
	require.JSONEq(t, `{"label":"retain"}`, saved)
	_, err = db.Exec(`ALTER TABLE bookmarks ALTER COLUMN label TYPE TEXT USING label::text`)
	require.NoError(t, err)
	for range 2 {
		store, err = NewBookmarkStore(db)
		require.NoError(t, err)
		require.NotNil(t, store)
	}
	bookmarks, err := store.ListBySession("saved")
	require.NoError(t, err)
	require.Len(t, bookmarks, 1)
}

func TestGatewayFreshMigrationsSerializeWithBootstrap(t *testing.T) {
	db, _ := testpg.Open(t)
	constructors := []func() error{
		func() error { return appdb.Migrate(db) },
		func() error { _, err := NewBookmarkStore(db); return err },
		func() error { _, err := NewInputHistory(db); return err },
		func() error { _, err := NewMessageDelivery(db); return err },
	}
	start := make(chan struct{})
	errs := make(chan error, 24)
	var wg sync.WaitGroup
	for i := range 24 {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; errs <- constructors[i%len(constructors)]() }()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	bookmarks, err := NewBookmarkStore(db)
	require.NoError(t, err)
	_, err = bookmarks.Save("persisted", "after-contention")
	require.NoError(t, err)
	history, err := NewInputHistory(db)
	require.NoError(t, err)
	require.NoError(t, history.Append(t.Context(), "chat", "saved"))
	delivery, err := NewMessageDelivery(db)
	require.NoError(t, err)
	id, err := delivery.RecordPending(t.Context(), "persisted", "chat", "saved")
	require.NoError(t, err)
	require.NoError(t, delivery.MarkFailed(t.Context(), id, "controlled failure"))
	require.NoError(t, appdb.Migrate(db))
	bookmarks, err = NewBookmarkStore(db)
	require.NoError(t, err)
	rows, err := bookmarks.ListBySession("persisted")
	require.NoError(t, err)
	require.Len(t, rows, 1)
	history, err = NewInputHistory(db)
	require.NoError(t, err)
	text, err := history.Get(t.Context(), "chat", 0)
	require.NoError(t, err)
	require.Equal(t, "saved", text)
	delivery, err = NewMessageDelivery(db)
	require.NoError(t, err)
	pending, err := delivery.GetUndelivered(t.Context(), "persisted")
	require.NoError(t, err)
	require.Len(t, pending, 1)
	require.Equal(t, id, pending[0].ID)
}

func TestGatewayMigrationsRejectMissingPool(t *testing.T) {
	bookmark, err := NewBookmarkStore(nil)
	require.Error(t, err)
	require.Nil(t, bookmark)
	history, err := NewInputHistory(nil)
	require.Error(t, err)
	require.Nil(t, history)
	delivery, err := NewMessageDelivery(nil)
	require.Error(t, err)
	require.Nil(t, delivery)
}

func TestGatewayMigrationLockDeadlineAndRetry(t *testing.T) {
	cases := []struct {
		name, table string
		migrate     func(context.Context, *sql.DB) error
	}{
		{"gateway_bookmarks", "bookmarks", dbschema.GatewayBookmarks},
		{"gateway_input_history", "input_history", dbschema.GatewayInputHistory},
		{"gateway_delivery", "message_delivery", dbschema.GatewayDelivery},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			db, _ := testpg.Open(t)
			tx, err := db.BeginTx(t.Context(), nil)
			require.NoError(t, err)
			defer tx.Rollback()
			_, err = tx.ExecContext(t.Context(), `SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || $1,0))`, ":overkill:"+test.name)
			require.NoError(t, err)
			ctx, cancel := context.WithTimeout(t.Context(), 80*time.Millisecond)
			defer cancel()
			started := time.Now()
			require.Error(t, test.migrate(ctx, db))
			require.ErrorIs(t, ctx.Err(), context.DeadlineExceeded)
			require.Less(t, time.Since(started), 2*time.Second)
			var table sql.NullString
			require.NoError(t, db.QueryRowContext(t.Context(), `SELECT to_regclass(current_schema() || '.' || $1)::text`, test.table).Scan(&table))
			require.False(t, table.Valid)
			require.NoError(t, tx.Rollback())
			require.NoError(t, test.migrate(t.Context(), db))
			require.NoError(t, test.migrate(t.Context(), db))
		})
	}
}

package memory

import (
	"database/sql"
	"sync"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/stretchr/testify/require"
)

func TestMemoryMigrationLateFailureRollbackAndExplicitRepair(t *testing.T) {
	db, _ := testpg.Open(t)
	_, err := db.ExecContext(t.Context(), `CREATE TABLE memory_items (
 id TEXT PRIMARY KEY, type TEXT NOT NULL DEFAULT '', content TEXT NOT NULL DEFAULT '',
 tags JSONB NOT NULL DEFAULT '[]', session_id TEXT NOT NULL DEFAULT '', metadata JSONB NOT NULL DEFAULT '{}');
 INSERT INTO memory_items(id,content) VALUES ('legacy','keep legacy memory')`)
	require.NoError(t, err)
	store, err := NewPostgresStore(db)
	require.Error(t, err)
	require.Nil(t, store)
	var content string
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT content FROM memory_items WHERE id='legacy'`).Scan(&content))
	require.Equal(t, "keep legacy memory", content)
	var present bool
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT EXISTS(SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='memory_items' AND column_name='content_hash')`).Scan(&present))
	require.False(t, present, "earlier added hash column must roll back after the final index fails")
	for _, name := range []string{"idx_memory_items_content_hash", "idx_memory_items_type", "idx_memory_items_session", "idx_memory_items_timestamp"} {
		require.NoError(t, db.QueryRowContext(t.Context(), `SELECT to_regclass($1) IS NOT NULL`, name).Scan(&present))
		require.False(t, present, "index %s must not survive failed migration", name)
	}
	// Explicit operator repair, rather than startup altering arbitrary legacy data.
	_, err = db.ExecContext(t.Context(), `ALTER TABLE memory_items ADD COLUMN timestamp TIMESTAMPTZ NOT NULL DEFAULT NOW()`)
	require.NoError(t, err)
	for range 2 {
		store, err = NewPostgresStore(db)
		require.NoError(t, err)
		require.NoError(t, store.Close())
	}
	legacy, err := store.Get(t.Context(), "legacy")
	require.NoError(t, err)
	require.Equal(t, "keep legacy memory", legacy.Content)
	var hash sql.NullString
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT content_hash FROM memory_items WHERE id='legacy'`).Scan(&hash))
	require.False(t, hash.Valid, "migration must preserve legacy NULL hashes")
	newer := time.Now().UTC()
	require.NoError(t, store.Store(t.Context(), &Memory{ID: "first", Content: "new deduplicated content", Timestamp: newer}))
	require.NoError(t, store.Store(t.Context(), &Memory{ID: "second", Content: "new deduplicated content", Timestamp: newer.Add(-time.Hour)}))
	var count int
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT count(*) FROM memory_items WHERE content='new deduplicated content'`).Scan(&count))
	require.Equal(t, 1, count)
	saved, err := store.Get(t.Context(), "first")
	require.NoError(t, err)
	require.WithinDuration(t, newer, saved.Timestamp, time.Microsecond)
}

func TestMemoryMigrationConcurrentConstructors(t *testing.T) {
	db, _ := testpg.Open(t)
	connections := make([]*sql.Conn, 16)
	for i := range connections {
		var err error
		connections[i], err = db.Conn(t.Context())
		require.NoError(t, err)
	}
	db.SetMaxIdleConns(16)
	for _, conn := range connections {
		require.NoError(t, conn.Close())
	}
	start := make(chan struct{})
	failures := make(chan error, 16)
	var workers sync.WaitGroup
	for range 16 {
		workers.Go(func() {
			<-start
			store, err := NewPostgresStore(db)
			if store != nil {
				store.Close()
			}
			failures <- err
		})
	}
	close(start)
	workers.Wait()
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
}

func TestMemoryMigrationNilDatabase(t *testing.T) {
	store, err := NewPostgresStore(nil)
	require.ErrorContains(t, err, "database is required")
	require.Nil(t, store)
}

func TestMemoryMigrationLockTimeoutAndRetry(t *testing.T) {
	db, _ := testpg.Open(t)
	tx, err := db.BeginTx(t.Context(), nil)
	require.NoError(t, err)
	defer tx.Rollback()
	_, err = tx.ExecContext(t.Context(), `SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || ':overkill:memory', 0))`)
	require.NoError(t, err)
	started := time.Now()
	store, err := NewPostgresStore(db)
	require.ErrorContains(t, err, "lock migration")
	require.Nil(t, store)
	require.Less(t, time.Since(started), 20*time.Second)
	var present bool
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT to_regclass('memory_items') IS NOT NULL`).Scan(&present))
	require.False(t, present)
	require.NoError(t, tx.Rollback())
	store, err = NewPostgresStore(db)
	require.NoError(t, err)
	defer store.Close()
	require.NoError(t, store.Store(t.Context(), &Memory{ID: "after-timeout", Content: "retained after retry"}))
	saved, err := store.Get(t.Context(), "after-timeout")
	require.NoError(t, err)
	require.Equal(t, "retained after retry", saved.Content)
}

func TestMemoryMigrationDuplicateHashesPreservedAndExplicitRepair(t *testing.T) {
	db, _ := testpg.Open(t)
	_, err := db.ExecContext(t.Context(), `CREATE TABLE memory_items (
 id TEXT PRIMARY KEY,type TEXT NOT NULL DEFAULT '',content TEXT NOT NULL DEFAULT '',content_hash TEXT,
 tags JSONB NOT NULL DEFAULT '[]',session_id TEXT NOT NULL DEFAULT '',timestamp TIMESTAMPTZ NOT NULL DEFAULT NOW(),metadata JSONB NOT NULL DEFAULT '{}');
 INSERT INTO memory_items(id,content,content_hash) VALUES ('a','first retained','duplicate'),('b','second retained','duplicate')`)
	require.NoError(t, err)
	store, err := NewPostgresStore(db)
	require.ErrorContains(t, err, "create content hash index")
	require.Nil(t, store)
	var count int
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT count(*) FROM memory_items WHERE content_hash='duplicate'`).Scan(&count))
	require.Equal(t, 2, count, "startup must not delete or rehash conflicting legacy rows")
	var content string
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT content FROM memory_items WHERE id='b'`).Scan(&content))
	require.Equal(t, "second retained", content)
	// Explicit fixture repair preserves both payloads and opts one old row out
	// of deduplication. Startup itself does not make this policy decision.
	_, err = db.ExecContext(t.Context(), `UPDATE memory_items SET content_hash=NULL WHERE id='b'`)
	require.NoError(t, err)
	store, err = NewPostgresStore(db)
	require.NoError(t, err)
	defer store.Close()
	var hash sql.NullString
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT content_hash FROM memory_items WHERE id='b'`).Scan(&hash))
	require.False(t, hash.Valid)
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT count(*) FROM memory_items`).Scan(&count))
	require.Equal(t, 2, count)
}

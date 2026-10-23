package dbschema_test

import (
	"database/sql"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/coordination"
	"github.com/Sahaj-Tech-ltd/overkill/internal/cost"
	"github.com/Sahaj-Tech-ltd/overkill/internal/dbschema"
	"github.com/Sahaj-Tech-ltd/overkill/internal/memory"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/stretchr/testify/require"
)

func execMatrix(t *testing.T, db *sql.DB, sql string) {
	t.Helper()
	_, err := db.ExecContext(t.Context(), sql)
	require.NoError(t, err)
}

// Fixtures remove only columns with explicit production upgrade statements.
// They model the supported predecessor shape, not every historical release.
func TestMigrationMatrixLegacySessionColumns(t *testing.T) {
	db, _ := testpg.Open(t)
	store, err := session.NewPostgresStore(db)
	require.NoError(t, err)
	original := session.NewSession(t.TempDir())
	original.Title = "preserved legacy session"
	require.NoError(t, store.Create(t.Context(), original))
	execMatrix(t, db, `ALTER TABLE sessions DROP COLUMN metadata, DROP COLUMN token_count, DROP COLUMN children`)
	for range 2 {
		store, err = session.NewPostgresStore(db)
		require.NoError(t, err)
	}
	loaded, err := store.Load(t.Context(), original.ID)
	require.NoError(t, err)
	require.Equal(t, original.Title, loaded.Title)
	require.Equal(t, original.Folder, loaded.Folder)
	require.Empty(t, loaded.Metadata)
	require.Empty(t, loaded.Children)
	loaded.Title = "first save"
	require.NoError(t, store.Save(t.Context(), loaded))
	loaded.Title = "second save"
	require.NoError(t, store.Save(t.Context(), loaded))
	loaded, err = store.Load(t.Context(), original.ID)
	require.NoError(t, err)
	require.Equal(t, "second save", loaded.Title)
}

func TestMigrationMatrixLegacyCostIntegers(t *testing.T) {
	db, _ := testpg.Open(t)
	tracker, err := cost.NewPostgresTracker(db, config.CostConfig{})
	require.NoError(t, err)
	require.NoError(t, tracker.Record(t.Context(), cost.Entry{ID: "legacy", SessionID: "session", InputTokens: 23, OutputTokens: 7, CachedTokens: 5, CostUSD: 0.125}))
	execMatrix(t, db, `ALTER TABLE cost_records ALTER COLUMN input_tokens TYPE INTEGER, ALTER COLUMN output_tokens TYPE INTEGER, ALTER COLUMN cached_tokens TYPE INTEGER`)
	for range 2 {
		tracker, err = cost.NewPostgresTracker(db, config.CostConfig{})
		require.NoError(t, err)
	}
	const large = 1 << 33
	require.NoError(t, tracker.Record(t.Context(), cost.Entry{ID: "wide", SessionID: "session", InputTokens: large, OutputTokens: large + 1, CachedTokens: large + 2, CostUSD: 0.25}))
	var input, output, cached int64
	var price float64
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT input_tokens,output_tokens,cached_tokens,cost_usd FROM cost_records WHERE id='legacy'`).Scan(&input, &output, &cached, &price))
	require.Equal(t, []int64{23, 7, 5}, []int64{input, output, cached})
	require.Equal(t, 0.125, price)
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT input_tokens,output_tokens,cached_tokens FROM cost_records WHERE id='wide'`).Scan(&input, &output, &cached))
	require.Equal(t, []int64{large, large + 1, large + 2}, []int64{input, output, cached})
}

func TestMigrationMatrixPreDedupMemory(t *testing.T) {
	db, _ := testpg.Open(t)
	store, err := memory.NewPostgresStore(db)
	require.NoError(t, err)
	require.NoError(t, store.Close())
	execMatrix(t, db, `ALTER TABLE memory_items DROP COLUMN content_hash; INSERT INTO memory_items(id,type,content) VALUES ('legacy','fact','preserved')`)
	for range 2 {
		store, err = memory.NewPostgresStore(db)
		require.NoError(t, err)
		require.NoError(t, store.Close())
	}
	legacy, err := store.Get(t.Context(), "legacy")
	require.NoError(t, err)
	require.Equal(t, "preserved", legacy.Content)
	require.NoError(t, store.Store(t.Context(), &memory.Memory{ID: "new", Content: "deduplicated content"}))
	require.NoError(t, store.Store(t.Context(), &memory.Memory{ID: "duplicate", Content: "deduplicated content"}))
	var count int
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT count(*) FROM memory_items`).Scan(&count))
	require.Equal(t, 2, count)
	var hash sql.NullString
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT content_hash FROM memory_items WHERE id='legacy'`).Scan(&hash))
	require.False(t, hash.Valid, "upgrade preserves legacy rows without claiming a content-hash backfill")
}

func TestMigrationMatrixCoordinationColumnDefaults(t *testing.T) {
	db, _ := testpg.Open(t)
	_, err := coordination.NewStore(t.Context(), db)
	require.NoError(t, err)
	execMatrix(t, db, `INSERT INTO coordination_peers(id,project_id,name,role,workspace) VALUES ('front','p','front','fixture','/fixture/front'),('back','p','back','fixture','/fixture/back');
 INSERT INTO coordination_messages(id,project_id,from_peer,to_peer,kind,conversation_id,payload,idempotency_key,request_hash,deadline) VALUES ('legacy','p','front','back','request','conversation','{"text":"preserved"}','legacy','hash',NOW()+INTERVAL '1 hour');
 ALTER TABLE coordination_messages DROP COLUMN required_contracts, DROP COLUMN depends_on, DROP COLUMN parent_message_id, DROP COLUMN request_depth, DROP COLUMN execution_attempts`)
	for range 2 {
		_, err = coordination.NewStore(t.Context(), db)
		require.NoError(t, err)
	}
	var dependencies, contracts, parent, payload string
	var depth, attempts, revision int
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT depends_on::text,required_contracts::text,parent_message_id,request_depth,execution_attempts,revision,payload::text FROM coordination_messages WHERE id='legacy'`).Scan(&dependencies, &contracts, &parent, &depth, &attempts, &revision, &payload))
	require.JSONEq(t, `[]`, dependencies)
	require.JSONEq(t, `[]`, contracts)
	require.Empty(t, parent)
	require.Zero(t, depth)
	require.Zero(t, attempts)
	require.Equal(t, 1, revision)
	require.JSONEq(t, `{"text":"preserved"}`, payload)
}

func TestMigrationMatrixSharedSchemaLateFailureRollbackAndRetry(t *testing.T) {
	db, _ := testpg.Open(t)
	// A JSON session_id cannot receive the final btree index. This deliberately
	// incompatible predecessor tests failure after all column additions ran.
	execMatrix(t, db, `CREATE TABLE suspensions(id TEXT PRIMARY KEY,session_id JSON NOT NULL,state JSONB NOT NULL DEFAULT '{}',suspended_at TIMESTAMPTZ NOT NULL DEFAULT NOW()); INSERT INTO suspensions(id,session_id,state) VALUES ('legacy','"session"','{"kept":true}')`)
	require.Error(t, dbschema.Suspensions(t.Context(), db))
	var count int
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT count(*) FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='suspensions' AND column_name IN ('tool_name','args','risk','call_data')`).Scan(&count))
	require.Zero(t, count, "earlier additions must roll back")
	var state string
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT state::text FROM suspensions WHERE id='legacy'`).Scan(&state))
	require.JSONEq(t, `{"kept":true}`, state)
	execMatrix(t, db, `ALTER TABLE suspensions ALTER COLUMN session_id TYPE TEXT USING session_id#>>'{}'`)
	require.NoError(t, dbschema.Suspensions(t.Context(), db))
	require.NoError(t, dbschema.Suspensions(t.Context(), db))
	var sessionID, tool string
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT session_id,tool_name,state::text FROM suspensions WHERE id='legacy'`).Scan(&sessionID, &tool, &state))
	require.Equal(t, "session", sessionID)
	require.Empty(t, tool)
	require.JSONEq(t, `{"kept":true}`, state)
}

func TestMigrationMatrixCoordinationLateFailureRollbackAndRetry(t *testing.T) {
	db, _ := testpg.Open(t)
	// The late usage-samples foreign key requires project_id/scope. An
	// incompatible preexisting relation must preserve its data and prevent a
	// partially initialized coordination service from escaping the constructor.
	execMatrix(t, db, `CREATE TABLE coordination_task_usage(marker TEXT PRIMARY KEY); INSERT INTO coordination_task_usage VALUES ('preserved')`)
	store, err := coordination.NewStore(t.Context(), db)
	require.Error(t, err)
	require.Nil(t, store)
	var marker string
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT marker FROM coordination_task_usage`).Scan(&marker))
	require.Equal(t, "preserved", marker)
	for _, relation := range []string{"coordination_peers", "coordination_messages", "coordination_contracts", "coordination_request_reviews", "coordination_usage_runs"} {
		var present bool
		require.NoError(t, db.QueryRowContext(t.Context(), `SELECT to_regclass($1) IS NOT NULL`, relation).Scan(&present))
		require.False(t, present, "transaction leaked %s", relation)
	}
	execMatrix(t, db, `ALTER TABLE coordination_task_usage RENAME TO incompatible_usage_backup`)
	for range 2 {
		store, err = coordination.NewStore(t.Context(), db)
		require.NoError(t, err)
		require.NotNil(t, store)
	}
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT marker FROM incompatible_usage_backup`).Scan(&marker))
	require.Equal(t, "preserved", marker)
}

func TestMigrationMatrixLegacyCallbackSubscription(t *testing.T) {
	db, _ := testpg.Open(t)
	_, err := coordination.NewStore(t.Context(), db)
	require.NoError(t, err)
	execMatrix(t, db, `INSERT INTO coordination_peers(id,project_id,name,role,workspace) VALUES ('peer','p','peer','fixture','/fixture');
 INSERT INTO coordination_callback_subscriptions(id,peer_id,destination,cursor,secret) VALUES ('legacy','peer','https://example.invalid/callback',12,'secret');
 DROP INDEX idx_coordination_callbacks_active_destination;
 ALTER TABLE coordination_callback_subscriptions DROP COLUMN signing_generation, DROP COLUMN last_attempt, ADD CONSTRAINT coordination_callback_subscriptions_peer_id_destination_key UNIQUE(peer_id,destination)`)
	for range 2 {
		_, err = coordination.NewStore(t.Context(), db)
		require.NoError(t, err)
	}
	var cursor, generation int64
	var secret []byte
	var epoch bool
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT cursor,secret,signing_generation,last_attempt='epoch'::timestamptz FROM coordination_callback_subscriptions WHERE id='legacy'`).Scan(&cursor, &secret, &generation, &epoch))
	require.EqualValues(t, 12, cursor)
	require.Equal(t, []byte("secret"), secret)
	require.EqualValues(t, 1, generation)
	require.True(t, epoch)
	// The replacement partial unique index allows a new active subscription
	// after the former one is disabled, without losing the old durable receipt.
	execMatrix(t, db, `UPDATE coordination_callback_subscriptions SET active=false WHERE id='legacy'; INSERT INTO coordination_callback_subscriptions(id,peer_id,destination,cursor,secret) VALUES ('replacement','peer','https://example.invalid/callback',12,'new-secret')`)
	_, err = db.ExecContext(t.Context(), `INSERT INTO coordination_callback_subscriptions(id,peer_id,destination,cursor,secret) VALUES ('duplicate','peer','https://example.invalid/callback',12,'new-secret')`)
	require.Error(t, err, "two active destinations must remain forbidden")
}

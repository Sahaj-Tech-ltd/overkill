package cost

import (
	"sync"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/stretchr/testify/require"
)

func TestCostMigrationRejectsIncompatibleLegacySchemaAndRollsBack(t *testing.T) {
	db := openCostDB(t)
	_, err := NewPostgresTracker(db, config.CostConfig{})
	require.NoError(t, err)
	_, err = db.ExecContext(t.Context(), `ALTER TABLE cost_records ALTER COLUMN input_tokens TYPE INTEGER, ALTER COLUMN output_tokens TYPE TEXT, ALTER COLUMN cached_tokens TYPE INTEGER; DROP INDEX idx_cost_records_session; DROP INDEX idx_cost_records_timestamp;
 INSERT INTO cost_records(id,input_tokens,output_tokens,cached_tokens,cost_usd) VALUES ('legacy',23,'unconvertible',5,0.125)`)
	require.NoError(t, err)
	tracker, err := NewPostgresTracker(db, config.CostConfig{})
	require.ErrorContains(t, err, "output token width")
	require.Nil(t, tracker, "startup must not expose a tracker after a failed ALTER")
	var inputType, outputType string
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT data_type FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='cost_records' AND column_name='input_tokens'`).Scan(&inputType))
	require.Equal(t, "integer", inputType, "first successful ALTER must roll back")
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT data_type FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='cost_records' AND column_name='output_tokens'`).Scan(&outputType))
	require.Equal(t, "text", outputType)
	var value string
	var amount float64
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT output_tokens,cost_usd FROM cost_records WHERE id='legacy'`).Scan(&value, &amount))
	require.Equal(t, "unconvertible", value)
	require.Equal(t, 0.125, amount)
	var indexPresent bool
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT to_regclass('idx_cost_records_session') IS NOT NULL`).Scan(&indexPresent))
	require.False(t, indexPresent)
	// Explicit fixture repair is an operator action. Startup itself neither casts
	// away malformed values nor deletes the affected receipt.
	_, err = db.ExecContext(t.Context(), `UPDATE cost_records SET output_tokens='7'; ALTER TABLE cost_records ALTER COLUMN output_tokens TYPE INTEGER USING output_tokens::integer`)
	require.NoError(t, err)
	tracker, err = NewPostgresTracker(db, config.CostConfig{})
	require.NoError(t, err)
	const large = 1 << 33
	require.NoError(t, tracker.Record(t.Context(), Entry{ID: "wide", InputTokens: large, OutputTokens: large + 1, CachedTokens: large + 2}))
	var in, out, cache int64
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT input_tokens,output_tokens,cached_tokens FROM cost_records WHERE id='legacy'`).Scan(&in, &out, &cache))
	require.Equal(t, []int64{23, 7, 5}, []int64{in, out, cache})
}

func TestCostMigrationConcurrentInitialization(t *testing.T) {
	db := openCostDB(t)
	const count = 8
	failures := make(chan error, count)
	var workers sync.WaitGroup
	for range count {
		workers.Go(func() { _, err := NewPostgresTracker(db, config.CostConfig{}); failures <- err })
	}
	workers.Wait()
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
	tracker, err := NewPostgresTracker(db, config.CostConfig{})
	require.NoError(t, err)
	require.NoError(t, tracker.Record(t.Context(), Entry{ID: "after-concurrency", InputTokens: 1 << 33}))
}

func TestCostMigrationNilDatabase(t *testing.T) {
	tracker, err := NewPostgresTracker(nil, config.CostConfig{})
	require.ErrorContains(t, err, "database is required")
	require.Nil(t, tracker)
}

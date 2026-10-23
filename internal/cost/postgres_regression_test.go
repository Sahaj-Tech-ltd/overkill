package cost

import (
	"context"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/stretchr/testify/require"
)

func TestCostForDateUsesUTCRegardlessOfDatabaseTimezone(t *testing.T) {
	database := openCostDB(t)
	database.SetMaxOpenConns(1)
	ctx := context.Background()
	_, err := database.ExecContext(ctx, `SET TIME ZONE 'Pacific/Honolulu'`)
	require.NoError(t, err)
	tracker, err := NewPostgresTracker(database, config.CostConfig{})
	require.NoError(t, err)
	start := time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)
	for i, timestamp := range []time.Time{start.Add(-time.Second), start, start.Add(23 * time.Hour), start.Add(24 * time.Hour)} {
		require.NoError(t, tracker.Record(ctx, Entry{ID: string(rune('a' + i)), Timestamp: timestamp, CostUSD: 1}))
	}
	total, err := tracker.costForDate(ctx, "2026-10-21")
	require.NoError(t, err)
	require.Equal(t, int64(2), total.RequestCount)
	require.Equal(t, float64(2), total.TotalUSD)
	_, err = tracker.costForDate(ctx, "invalid")
	require.ErrorContains(t, err, "invalid UTC date")
}

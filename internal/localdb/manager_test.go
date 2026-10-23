package localdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func nativeConfig(t *testing.T) Config {
	t.Helper()
	bin := os.Getenv("OVERKILL_POSTGRES_BIN_DIR")
	if bin == "" {
		if os.Getenv("OVERKILL_REQUIRE_LOCALDB_TEST") == "1" {
			t.Fatal("OVERKILL_POSTGRES_BIN_DIR required")
		}
		t.Skip("native bundled PostgreSQL not configured")
	}
	return Config{BinDir: bin, DataDir: filepath.Join(t.TempDir(), "App Data With Spaces", "postgres"), StartupTimeout: 20 * time.Second, ShutdownTimeout: 5 * time.Second}
}

func startNative(t *testing.T, cfg Config) (*Manager, ConnectionInfo) {
	t.Helper()
	m, err := NewManager(cfg)
	require.NoError(t, err)
	info, err := m.Start(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		require.NoError(t, m.Stop(ctx))
	})
	return m, info
}

func connectNative(t *testing.T, info ConnectionInfo) *sql.DB {
	t.Helper()
	db, err := sql.Open("postgres", info.DSN())
	require.NoError(t, err)
	require.NoError(t, db.PingContext(t.Context()))
	t.Cleanup(func() { db.Close() })
	return db
}

func TestNativePersistRestartAndExclusiveOwnership(t *testing.T) {
	cfg := nativeConfig(t)
	m, info := startNative(t, cfg)
	db := connectNative(t, info)
	_, err := db.ExecContext(t.Context(), `CREATE TABLE durable_test(value text); INSERT INTO durable_test VALUES('survived')`)
	require.NoError(t, err)
	other, err := NewManager(cfg)
	require.NoError(t, err)
	_, err = other.Start(t.Context())
	require.ErrorIs(t, err, ErrInUse)
	require.NotContains(t, fmt.Sprintf("%v %#v", info, info), info.password)
	db.Close()
	require.NoError(t, m.Stop(t.Context()))
	for range 3 {
		info, err = other.Start(t.Context())
		require.NoError(t, err)
		db = connectNative(t, info)
		var value string
		require.NoError(t, db.QueryRowContext(t.Context(), `SELECT value FROM durable_test`).Scan(&value))
		require.Equal(t, "survived", value)
		db.Close()
		require.NoError(t, other.Stop(t.Context()))
	}
	data, err := os.ReadFile(cfg.DataDir + ".manager.json")
	require.NoError(t, err)
	require.Contains(t, string(data), `"phase":"ready"`)
	stat, err := os.Stat(cfg.DataDir + ".manager.json")
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), stat.Mode().Perm())
}

func TestNativePreservesUnmanagedAndCorruptClusters(t *testing.T) {
	cfg := nativeConfig(t)
	require.NoError(t, os.MkdirAll(cfg.DataDir, 0o700))
	marker := filepath.Join(cfg.DataDir, "user-data")
	require.NoError(t, os.WriteFile(marker, []byte("preserve"), 0o600))
	m, err := NewManager(cfg)
	require.NoError(t, err)
	_, err = m.Start(t.Context())
	require.ErrorContains(t, err, "no ownership metadata")
	data, err := os.ReadFile(marker)
	require.NoError(t, err)
	require.Equal(t, "preserve", string(data))
	require.NoError(t, os.WriteFile(cfg.DataDir+".manager.json", []byte("broken"), 0o600))
	_, err = m.Start(t.Context())
	require.ErrorContains(t, err, "corrupt")
	data, err = os.ReadFile(marker)
	require.NoError(t, err)
	require.Equal(t, "preserve", string(data))
}

func TestNativeLifetimeCancellationReapsAndRecovers(t *testing.T) {
	cfg := nativeConfig(t)
	m, err := NewManager(cfg)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	info, err := m.Start(ctx)
	require.NoError(t, err)
	db := connectNative(t, info)
	_, err = db.ExecContext(t.Context(), `CREATE TABLE crash_test(value text); INSERT INTO crash_test VALUES('committed')`)
	require.NoError(t, err)
	db.Close()
	run := m.run
	cancel()
	select {
	case <-run.exited:
	case <-time.After(5 * time.Second):
		t.Fatal("native process did not exit after cancellation")
	}
	require.NoError(t, m.Stop(t.Context()))
	info, err = m.Start(t.Context())
	require.NoError(t, err)
	db = connectNative(t, info)
	var value string
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT value FROM crash_test`).Scan(&value))
	require.Equal(t, "committed", value)
	db.Close()
	require.NoError(t, m.Stop(t.Context()))
}

func TestInvalidPathsAndCancelledStart(t *testing.T) {
	_, err := NewManager(Config{BinDir: "relative", DataDir: "relative"})
	require.Error(t, err)
	cfg := nativeConfig(t)
	m, err := NewManager(cfg)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = m.Start(ctx)
	require.True(t, errors.Is(err, context.Canceled))
	_, err = os.Stat(cfg.DataDir)
	require.True(t, os.IsNotExist(err))
	require.NoError(t, m.Stop(t.Context()))
}

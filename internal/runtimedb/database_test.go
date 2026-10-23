package runtimedb

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/db"
	"github.com/stretchr/testify/require"
)

func managedOptions(t *testing.T) Options {
	t.Helper()
	bin := os.Getenv("OVERKILL_POSTGRES_BIN_DIR")
	if bin == "" {
		if os.Getenv("OVERKILL_REQUIRE_LOCALDB_TEST") == "1" {
			t.Fatal("OVERKILL_POSTGRES_BIN_DIR required")
		}
		t.Skip("native PostgreSQL runtime not configured")
	}
	return Options{Managed: true, BinDir: bin, HomeDir: filepath.Join(t.TempDir(), "Empty Profile With Spaces"), Required: true}
}

func TestHeadlessAndExplicitOptIn(t *testing.T) {
	owner, err := Open(t.Context(), Options{})
	require.NoError(t, err)
	require.Nil(t, owner.DB)
	require.NoError(t, owner.Close())
	_, err = Open(t.Context(), Options{Required: true})
	require.ErrorContains(t, err, "PostgreSQL required")
	_, err = Open(t.Context(), Options{Managed: true})
	require.ErrorContains(t, err, "no binaries are downloaded")
}

func TestNativeFreshProfileMigrationsPersistenceAndDrain(t *testing.T) {
	options := managedOptions(t)
	parent, cancel := context.WithCancel(t.Context())
	owner, err := Open(parent, options)
	require.NoError(t, err)
	t.Cleanup(func() { _ = owner.Close() })
	// This table is created by the production migration before callers see DB.
	_, err = owner.DB.ExecContext(t.Context(), `INSERT INTO goals(session_id,text) VALUES('managed-relaunch','retained')`)
	require.NoError(t, err)
	cancel()
	<-owner.Context.Done()
	// Application cancellation allows stores/agents to drain against a live DB.
	var value string
	require.NoError(t, owner.DB.QueryRowContext(t.Context(), `SELECT text FROM goals WHERE session_id='managed-relaunch'`).Scan(&value))
	require.Equal(t, "retained", value)
	require.NoError(t, owner.Close())
	_, err = os.Stat(filepath.Join(options.HomeDir, "postgres", "postmaster.pid"))
	require.True(t, os.IsNotExist(err), "owned postmaster must be reaped")
	restarted, err := Open(t.Context(), options)
	require.NoError(t, err)
	defer func() { require.NoError(t, restarted.Close()) }()
	require.NoError(t, restarted.DB.QueryRowContext(t.Context(), `SELECT text FROM goals WHERE session_id='managed-relaunch'`).Scan(&value))
	require.Equal(t, "retained", value)
}

func TestNativeChildCrashCancelsApplicationAndReturnsFailure(t *testing.T) {
	options := managedOptions(t)
	owner, err := Open(t.Context(), options)
	require.NoError(t, err)
	t.Cleanup(func() { _ = owner.Close() })
	stop := exec.CommandContext(t.Context(), filepath.Join(options.BinDir, "pg_ctl"), "-D", filepath.Join(options.HomeDir, "postgres"), "-m", "immediate", "-w", "stop")
	output, err := stop.CombinedOutput()
	require.NoError(t, err, string(output))
	select {
	case <-owner.Context.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("owned database death did not cancel application")
	}
	require.ErrorContains(t, owner.Close(), "owned database")
	// Crash recovery and owner lease release must permit a clean relaunch.
	restarted, err := Open(t.Context(), options)
	require.NoError(t, err)
	require.NoError(t, restarted.Close())
}

func TestCancelledStartupDoesNotLeaveOwnedProcess(t *testing.T) {
	options := managedOptions(t)
	parent, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := Open(parent, options)
	require.Error(t, err)
	_, err = os.Stat(filepath.Join(options.HomeDir, "postgres", "postmaster.pid"))
	require.True(t, os.IsNotExist(err))
	// A cancelled handshake must release the lease for the next launch.
	owner, err := Open(t.Context(), options)
	require.NoError(t, err)
	require.NoError(t, owner.Close())
}

func TestExternalURLTakesPriorityAndIsNeverStopped(t *testing.T) {
	connection := os.Getenv("DATABASE_URL")
	if connection == "" {
		t.Skip("explicit external test PostgreSQL URL not configured")
	}
	home := filepath.Join(t.TempDir(), "unused")
	owner, err := Open(t.Context(), Options{URL: connection, Managed: true, BinDir: "/unavailable", HomeDir: home, Required: true})
	require.NoError(t, err)
	require.NoError(t, owner.Close())
	_, err = os.Stat(home)
	require.True(t, os.IsNotExist(err), "external selection must not initialize managed storage")
	pool, err := db.OpenContext(t.Context(), connection)
	require.NoError(t, err, "closing an external pool must leave server running")
	require.NoError(t, pool.Close())
}

func TestMissingRuntimeDoesNotCreateCluster(t *testing.T) {
	options := Options{Managed: true, BinDir: filepath.Join(t.TempDir(), "not-installed"), DataDir: filepath.Join(t.TempDir(), "cluster")}
	_, err := Open(t.Context(), options)
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "unavailable"))
	_, err = os.Stat(options.DataDir)
	require.True(t, os.IsNotExist(err))
}

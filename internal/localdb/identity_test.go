package localdb

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNativeAuthenticatedPIDFileIdentity(t *testing.T) {
	m, info := startNative(t, nativeConfig(t))
	identity, err := readPostmaster(m.config.DataDir)
	require.NoError(t, err)
	require.NoError(t, m.verifyConnection(t.Context(), info, identity))
	for _, field := range []string{"pid", "start", "port", "directory"} {
		t.Run(field, func(t *testing.T) {
			altered := identity
			switch field {
			case "pid":
				altered.PID++
			case "start":
				altered.Started++
			case "port":
				altered.Port++
			case "directory":
				altered.DataDir = filepath.Join(altered.DataDir, "other")
			}
			err := m.verifyConnection(t.Context(), info, altered)
			require.Error(t, err)
			require.Contains(t, err.Error(), "differs from the owned cluster")
			require.NotContains(t, err.Error(), info.password)
		})
	}
	wrongInfo := info
	wrongInfo.DataDir = filepath.Join(info.DataDir, "other")
	require.ErrorContains(t, m.verifyConnection(t.Context(), wrongInfo, identity), "data directory differs")
	// The SQL connection independently reads the live server's own PID file.
	// Replacing only the expected in-memory identity cannot authenticate it.
	db := connectNative(t, info)
	var sqlFile string
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT pg_read_file('postmaster.pid',0,8192)`).Scan(&sqlFile))
	fromSQL, err := parsePostmaster([]byte(sqlFile), m.config.DataDir)
	require.NoError(t, err)
	require.Equal(t, identity, fromSQL)
	db.Close()
	require.NoError(t, m.Stop(t.Context()))
}

func TestPostmasterIdentityParserIsBoundedAndStrict(t *testing.T) {
	dir := t.TempDir()
	valid := fmt.Sprintf("123\n%s\n456\n5432\n", dir)
	got, err := parsePostmaster([]byte(valid), dir)
	require.NoError(t, err)
	require.Equal(t, postmaster{PID: 123, DataDir: dir, Started: 456, Port: 5432}, got)
	fromWindows, err := parsePostmaster([]byte(strings.ReplaceAll(valid, "\n", "\r\n")), dir)
	require.NoError(t, err)
	require.Equal(t, got, fromWindows)
	for _, bad := range []string{"", "123\n", strings.Replace(valid, "123", "0", 1), strings.Replace(valid, "456", "bad", 1), strings.Replace(valid, "5432", "65536", 1), strings.Replace(valid, dir, dir+"-other", 1), valid + strings.Repeat("x", 8193)} {
		_, err := parsePostmaster([]byte(bad), dir)
		require.Error(t, err)
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "postmaster.pid"), []byte(valid+strings.Repeat("x", 8193)), 0600))
	_, err = readPostmaster(dir)
	require.ErrorContains(t, err, "exceeds 8192")
}

func TestReadinessTimeoutReportsLastCheckWithoutCredential(t *testing.T) {
	dir := t.TempDir()
	m := &Manager{config: Config{DataDir: dir}}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "postmaster.pid"), []byte(fmt.Sprintf("123\n%s\n456\n5432\n", dir)), 0600))
	run := &running{info: ConnectionInfo{DataDir: dir, Port: 5433, password: "never-print-this-private-password"}, exited: make(chan struct{})}
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Millisecond)
	defer cancel()
	err := m.waitReady(ctx, run, &metadata{})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.ErrorContains(t, err, "last readiness check: localdb: PID file identity differs")
	require.NotContains(t, err.Error(), run.info.password)
}

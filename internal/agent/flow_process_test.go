package agent

import (
	"database/sql"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// This test entry is a subprocess harness, not an independent acceptance gate.
func TestDurableFlowLeaseChildProcess(t *testing.T) {
	dsn := os.Getenv("OVERKILL_FLOW_TEST_CHILD_DSN")
	if dsn == "" {
		return
	}
	database, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer database.Close()
	queue, err := NewFlowQueue(database, time.Millisecond)
	require.NoError(t, err)
	lease, err := queue.Claim(t.Context(), os.Getenv("OVERKILL_FLOW_TEST_CHILD_ID"))
	require.NoError(t, err)
	require.NotNil(t, lease)
	defer lease.Close()
	require.NoError(t, os.WriteFile(os.Getenv("OVERKILL_FLOW_TEST_CHILD_READY"), []byte("claimed"), 0o600))
	<-t.Context().Done()
}
func TestDurableFlowRealProcessDeathReleasesLeaseWithoutReplayingTools(t *testing.T) {
	q, bound, state := flowQueueFixture(t)
	require.NoError(t, bound.Save(state))
	time.Sleep(3 * time.Millisecond)
	var schema string
	require.NoError(t, q.db.QueryRow(`SELECT current_schema()`).Scan(&schema))
	base := os.Getenv("PG_TEST_URL")
	if base == "" {
		base = os.Getenv("DATABASE_URL")
	}
	dsn, err := url.Parse(base)
	require.NoError(t, err)
	require.Contains(t, []string{"postgres", "postgresql"}, dsn.Scheme, "subprocess acceptance requires the configured URL-form test database")
	query := dsn.Query()
	query.Set("search_path", schema)
	dsn.RawQuery = query.Encode()
	ready := filepath.Join(t.TempDir(), "ready")
	child := exec.Command(os.Args[0], "-test.run=^TestDurableFlowLeaseChildProcess$", "-test.timeout=30s")
	child.Env = append(os.Environ(), "OVERKILL_FLOW_TEST_CHILD_DSN="+dsn.String(), "OVERKILL_FLOW_TEST_CHILD_ID="+state.ID, "OVERKILL_FLOW_TEST_CHILD_READY="+ready)
	child.Stdout = io.Discard
	child.Stderr = io.Discard
	require.NoError(t, child.Start())
	waited := false
	t.Cleanup(func() {
		if !waited {
			_ = child.Process.Kill()
			_ = child.Wait()
		}
	})
	require.Eventually(t, func() bool { raw, err := os.ReadFile(ready); return err == nil && string(raw) == "claimed" }, 5*time.Second, 10*time.Millisecond)
	duplicate, err := q.Claim(t.Context(), state.ID)
	require.NoError(t, err)
	require.Nil(t, duplicate, "a live process must retain its exclusive flow lease")
	require.NoError(t, child.Process.Kill())
	require.Error(t, child.Wait())
	waited = true
	require.Eventually(t, func() bool {
		lease, _ := q.Claim(t.Context(), state.ID)
		if lease != nil {
			lease.Close()
			return false
		}
		jobs, err := q.ListSession(t.Context(), state.SessionID)
		return err == nil && len(jobs) == 1 && jobs[0].Status == "manual_review"
	}, 3*time.Second, 10*time.Millisecond)
	saved, err := bound.Load(state.ID)
	require.NoError(t, err)
	require.Equal(t, 1, saved.Resumes, "lost executing owner must not be replayed as another automatic attempt")
}

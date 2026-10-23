//go:build darwin || linux || freebsd || openbsd || netbsd || dragonfly

package web

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCanvasSnapshotShutdownCancelsOwnedBrowserStartup(t *testing.T) {
	srv, store, _, sid := snapshotFixture(t)
	snapshotScene(t, store, sid)
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "owned-browser.pid")
	executable := filepath.Join(dir, "delayed-browser")
	quoted := "'" + strings.ReplaceAll(pidFile, "'", "'\\''") + "'"
	require.NoError(t, os.WriteFile(executable, []byte("#!/bin/sh\nprintf '%s' \"$$\" > "+quoted+"\nexec /bin/sleep 20\n"), 0700))
	srv.cfg.CanvasChromePath = executable
	finished := make(chan int, 1)
	go func() { finished <- snapshotHTTP(srv, "sessionId="+sid+"&surfaceId=view").Code }()
	var pid int
	require.Eventually(t, func() bool {
		data, err := os.ReadFile(pidFile)
		if err != nil {
			return false
		}
		pid, err = strconv.Atoi(strings.TrimSpace(string(data)))
		return err == nil && pid > 0
	}, 5*time.Second, 10*time.Millisecond)
	t.Cleanup(func() {
		if process, err := os.FindProcess(pid); err == nil {
			_ = process.Kill()
		}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	require.NoError(t, srv.Shutdown(ctx))
	select {
	case status := <-finished:
		require.Equal(t, 503, status)
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown did not cancel in-flight snapshot browser startup")
	}
	require.Eventually(t, func() bool { return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) }, 3*time.Second, 10*time.Millisecond, "only the exact test-owned browser process must be reaped")
}

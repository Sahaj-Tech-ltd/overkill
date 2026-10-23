//go:build unix

package sync

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestGitStalledRemoteCancellationTerminatesSSHDescendant(t *testing.T) {
	backend := gitFixture(t)
	helper := filepath.Join(t.TempDir(), "ssh-helper")
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	require.NoError(t, os.WriteFile(helper, []byte("#!/bin/sh\n/bin/sleep 30 &\nchild=$!\nprintf '%s' \"$child\" > \"$OVERKILL_TEST_SSH_PID\"\nwait \"$child\"\n"), 0o700))
	t.Setenv("GIT_SSH_COMMAND", helper)
	t.Setenv("GIT_SSH_VARIANT", "ssh")
	t.Setenv("OVERKILL_TEST_SSH_PID", pidFile)
	_, err := runGit(backend.dir, "remote", "add", "origin", "ssh://fixture.invalid/repository")
	require.NoError(t, err)
	backend.remote = "ssh://fixture.invalid/repository"
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	var pid int
	joined := false
	t.Cleanup(func() {
		cancel()
		if !joined {
			select {
			case <-result:
			case <-time.After(3 * time.Second):
				t.Error("Git fixture did not join during cleanup")
			}
		}
		if pid > 0 {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
	go func() { _, _, err := backend.Pull(ctx, "fixture"); result <- err }()
	// Establish fixture readiness separately from the cancellation deadline.
	// Slow scheduling must not turn a missing helper into false cancellation
	// evidence; an early Git error should be reported instead of waiting blindly.
	startup := time.NewTimer(15 * time.Second)
	defer startup.Stop()
	poll := time.NewTicker(10 * time.Millisecond)
	defer poll.Stop()
ready:
	for {
		select {
		case err := <-result:
			joined = true
			t.Fatalf("Git exited before SSH helper readiness: %v", err)
		case <-startup.C:
			t.Fatal("SSH helper did not publish its PID before fixture startup deadline")
		case <-poll.C:
			data, err := os.ReadFile(pidFile)
			if err != nil {
				continue
			}
			pid, _ = strconv.Atoi(strings.TrimSpace(string(data)))
			if pid > 0 {
				break ready
			}
		}
	}
	cancel()
	select {
	case err := <-result:
		joined = true
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(3 * time.Second):
		t.Fatal("stalled Git ignored cancellation")
	}
	require.Eventually(t, func() bool { return syscall.Kill(pid, 0) == syscall.ESRCH }, 3*time.Second, 10*time.Millisecond, "SSH descendant survived canceled Git")
}

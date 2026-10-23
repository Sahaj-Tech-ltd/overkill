//go:build unix

package audit

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestCancelledAuditTerminatesProjectScriptDescendants(t *testing.T) {
	root := t.TempDir()
	pidFile := filepath.Join(root, "child.pid")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := runCmd(ctx, root, "sh", "-c", "sleep 60 & echo $! > child.pid; wait"); done <- err }()
	deadline := time.Now().Add(3 * time.Second)
	var childPID int
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(pidFile); err == nil {
			childPID, _ = strconv.Atoi(strings.TrimSpace(string(data)))
			if childPID > 0 {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if childPID == 0 {
		t.Fatal("script child never started")
	}
	defer syscall.Kill(childPID, syscall.SIGKILL)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("audit script did not stop")
	}
	// On macOS the killed child can briefly remain a zombie until launchd reaps
	// it. A live child still responding to execution must not survive cancellation.
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if errors.Is(syscall.Kill(childPID, 0), syscall.ESRCH) {
			return
		}
		if state, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(childPID)).Output(); err == nil && strings.HasPrefix(strings.TrimSpace(string(state)), "Z") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("audit script child survived cancellation")
}

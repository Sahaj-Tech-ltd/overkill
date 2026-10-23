//go:build unix

package processrunner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCancellationStopsControlledGrandchild(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "grandchild.pid")
	heartbeat := filepath.Join(dir, "heartbeat")
	executable, err := os.Executable()
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.Command(executable, "-test.run=^TestProcessTreeHelper$")
	cmd.Env = append(os.Environ(), "OVERKILL_PROCESS_HELPER=root", "OVERKILL_PROCESS_PID="+pidPath, "OVERKILL_PROCESS_HEARTBEAT="+heartbeat)
	done := make(chan error, 1)
	go func() { done <- Run(ctx, cmd) }()
	deadline := time.Now().Add(5 * time.Second)
	var before []byte
	for time.Now().Before(deadline) {
		before, err = os.ReadFile(heartbeat)
		if _, pidErr := os.Stat(pidPath); err == nil && len(before) > 0 && pidErr == nil {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("root exited before descendant started: %v", err)
		case <-time.After(10 * time.Millisecond):
		}
	}
	require.NotEmpty(t, before, "grandchild did not begin its controlled work")
	pid, err := os.ReadFile(pidPath)
	require.NoError(t, err)
	require.NotEmpty(t, pid)
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled process tree did not drain")
	}
	after, err := os.ReadFile(heartbeat)
	require.NoError(t, err)
	time.Sleep(150 * time.Millisecond)
	final, err := os.ReadFile(heartbeat)
	require.NoError(t, err)
	require.Equal(t, string(after), string(final), "grandchild continued running after cancellation completed")
}

func TestProcessTreeHelper(t *testing.T) {
	switch os.Getenv("OVERKILL_PROCESS_HELPER") {
	case "root":
		executable, err := os.Executable()
		require.NoError(t, err)
		cmd := exec.Command(executable, "-test.run=^TestProcessTreeHelper$")
		environment := []string{}
		for _, entry := range os.Environ() {
			if !strings.HasPrefix(entry, "OVERKILL_PROCESS_HELPER=") {
				environment = append(environment, entry)
			}
		}
		cmd.Env = append(environment, "OVERKILL_PROCESS_HELPER=leaf")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		require.NoError(t, cmd.Start())
		require.NoError(t, os.WriteFile(os.Getenv("OVERKILL_PROCESS_PID"), []byte(strconv.Itoa(cmd.Process.Pid)), 0600))
		_ = cmd.Wait()
	case "leaf":
		file, err := os.OpenFile(os.Getenv("OVERKILL_PROCESS_HEARTBEAT"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		require.NoError(t, err)
		defer file.Close()
		for i := 0; i < 3000; i++ {
			_, err = file.WriteString("tick\n")
			require.NoError(t, err)
			time.Sleep(10 * time.Millisecond)
		}
	}
}

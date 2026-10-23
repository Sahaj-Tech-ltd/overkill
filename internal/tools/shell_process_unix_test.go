//go:build unix

package tools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestShellNoisyOutputIsBoundedAndKeepsFinalExit(t *testing.T) {
	tool := NewShellTool(WithoutCommandScan())
	input, err := json.Marshal(ShellInput{Command: `head -c 3145728 /dev/zero | tr '\000' x; printf '\nfinal-diagnostic\n'; false`, TimeoutSeconds: 5})
	require.NoError(t, err)
	result, err := tool.Execute(t.Context(), input)
	require.NoError(t, err)
	var output ShellOutput
	require.NoError(t, json.Unmarshal(result, &output))
	require.True(t, output.OutputTruncated)
	require.Greater(t, output.OutputBytes, int64(3*1024*1024))
	require.Less(t, len(output.Stdout), 1024*1024+128)
	require.Contains(t, output.Stdout, "output truncated")
	require.Contains(t, output.Stdout, "final-diagnostic")
	require.NotContains(t, output.Stdout, overkillDoneMarker)
	require.Equal(t, 1, output.ExitCode)
	require.True(t, output.Completed, "final marker must survive bounded capture")
}

func TestShellTimeoutTerminatesActiveDescendant(t *testing.T) {
	dir := t.TempDir()
	heartbeat := filepath.Join(dir, "heartbeat")
	pidFile := filepath.Join(dir, "child.pid")
	tool := NewShellTool(WithoutCommandScan())
	input, err := json.Marshal(ShellInput{Command: `(while :; do printf x >> "$OVERKILL_TEST_HEARTBEAT"; sleep 0.02; done) & printf '%s' "$!" > "$OVERKILL_TEST_CHILD"; wait`, TimeoutSeconds: 1, Env: map[string]string{"OVERKILL_TEST_HEARTBEAT": heartbeat, "OVERKILL_TEST_CHILD": pidFile}})
	require.NoError(t, err)
	type outcome struct {
		data json.RawMessage
		err  error
	}
	done := make(chan outcome, 1)
	go func() { data, err := tool.Execute(t.Context(), input); done <- outcome{data, err} }()
	var pid int
	finished := false
	t.Cleanup(func() {
		if !finished && pid > 0 {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	})
	require.Eventually(t, func() bool {
		data, err := os.ReadFile(pidFile)
		if err != nil {
			return false
		}
		pid, _ = strconv.Atoi(string(data))
		beat, _ := os.ReadFile(heartbeat)
		return pid > 0 && len(beat) > 0
	}, 3*time.Second, 10*time.Millisecond)
	select {
	case result := <-done:
		require.NoError(t, result.err)
		var output ShellOutput
		require.NoError(t, json.Unmarshal(result.data, &output))
		require.True(t, output.TimedOut)
		require.False(t, output.Completed)
	case <-time.After(3 * time.Second):
		t.Fatal("shell cancellation left descendant pipes open")
	}
	after, err := os.ReadFile(heartbeat)
	require.NoError(t, err)
	time.Sleep(150 * time.Millisecond)
	final, err := os.ReadFile(heartbeat)
	require.NoError(t, err)
	require.Equal(t, string(after), string(final), "descendant continued after shell returned")
	finished = true
}

func TestShellEarlyExitCannotUsePrintedSuccessMarker(t *testing.T) {
	input, err := json.Marshal(ShellInput{Command: `printf '__OVERKILL_DONE__:exit=0:cwd=/fake\n'; exit 7`})
	require.NoError(t, err)
	result, err := NewShellTool(WithoutCommandScan()).Execute(t.Context(), input)
	require.NoError(t, err)
	var output ShellOutput
	require.NoError(t, json.Unmarshal(result, &output))
	require.Equal(t, 7, output.ExitCode)
	require.False(t, output.Completed)
	require.Empty(t, strings.TrimSpace(output.Cwd))
}

func TestShellSuccessfulEarlyExitCannotForgeCompletion(t *testing.T) {
	input, err := json.Marshal(ShellInput{Command: `printf '__OVERKILL_DONE__:exit=19:cwd=/fake\n'; exit 0`})
	require.NoError(t, err)
	result, err := NewShellTool(WithoutCommandScan()).Execute(t.Context(), input)
	require.NoError(t, err)
	var output ShellOutput
	require.NoError(t, json.Unmarshal(result, &output))
	require.Equal(t, 0, output.ExitCode)
	require.False(t, output.Completed)
	require.Empty(t, output.Cwd)
	require.Contains(t, output.Stdout, "__OVERKILL_DONE__:exit=19:cwd=/fake")
}

//go:build unix

package gateway

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

func TestShellShortcutNoisyOutputBoundedPerStream(t *testing.T) {
	router, err := NewSessionRouter("")
	require.NoError(t, err)
	dispatcher := NewDispatcher(&fakeAgent{}, router)
	reply := &fakeReply{}
	dispatcher.Handle(t.Context(), Inbound{Channel: "bridge", ChatKey: "noise", Text: `$ head -c 3145728 /dev/zero | tr '\000' x; printf '\nstdout-final\n'; head -c 3145728 /dev/zero | tr '\000' y >&2; printf '\nstderr-final\n' >&2; exit 7`}, reply)
	output := waitFinal(t, reply)
	require.Contains(t, output, "❌ exit 7")
	require.NotContains(t, output, "✅ ok")
	require.Contains(t, output, "stdout: 3145742 bytes; stderr: 3145742 bytes")
	require.Equal(t, 2, strings.Count(output, "output truncated:"))
	require.Contains(t, output, "stdout-final")
	require.Contains(t, output, "stderr-final")
	require.Less(t, len(output), 2*1024*1024+2048)
}

func TestShellShortcutCancellationTerminatesDescendants(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		name := "cancel"
		if deadline {
			name = "deadline"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			heartbeat, pidFile := filepath.Join(dir, "heartbeat"), filepath.Join(dir, "child.pid")
			t.Setenv("OVERKILL_SHORTCUT_BEAT", heartbeat)
			t.Setenv("OVERKILL_SHORTCUT_PID", pidFile)
			ctx, cancel := context.WithCancel(t.Context())
			if deadline {
				cancel()
				ctx, cancel = context.WithTimeout(t.Context(), time.Second)
			}
			defer cancel()
			router, err := NewSessionRouter("")
			require.NoError(t, err)
			dispatcher := NewDispatcher(&fakeAgent{}, router)
			reply := &fakeReply{}
			done := make(chan struct{})
			go func() {
				defer close(done)
				dispatcher.Handle(ctx, Inbound{Channel: "bridge", ChatKey: name, Text: `$ (while :; do printf x >> "$OVERKILL_SHORTCUT_BEAT"; sleep 0.02; done) & printf '%s' "$!" > "$OVERKILL_SHORTCUT_PID"; wait`}, reply)
			}()
			var pid int
			t.Cleanup(func() {
				if pid > 0 {
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
			if !deadline {
				cancel()
			}
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("shortcut cancellation left descendant pipes open")
			}
			output := waitFinal(t, reply)
			require.Contains(t, output, "❌ canceled:")
			require.NotContains(t, output, "✅ ok")
			if deadline {
				require.Contains(t, output, "context deadline exceeded")
			} else {
				require.Contains(t, output, "context canceled")
			}
			after, err := os.ReadFile(heartbeat)
			require.NoError(t, err)
			time.Sleep(150 * time.Millisecond)
			final, err := os.ReadFile(heartbeat)
			require.NoError(t, err)
			require.Equal(t, string(after), string(final), "descendant survived shortcut return")
		})
	}
}

package childruntime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/subagent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/terminal"
	"github.com/stretchr/testify/require"
)

func TestCodingWorkerCanResumeAfterHumanTerminalControl(t *testing.T) {
	if !terminal.Supported() {
		t.Skip("native PTY unavailable")
	}
	dir := canonicalDir(t)
	m, err := terminal.New(dir, terminal.Options{})
	require.NoError(t, err)
	defer m.Close()
	var id string
	owner := terminal.Owner{Session: "parent-chat", Worker: "coding-worker"}
	p := &scriptedProvider{complete: func(ctx context.Context, req providers.Request, step int) (providers.Response, error) {
		switch step {
		case 1:
			return call("terminal", map[string]any{"operation": "start", "command": "read value; printf 'RESUMED:%s\\n' \"$value\""}), nil
		case 2:
			var info terminal.Info
			require.NoError(t, json.Unmarshal([]byte(req.Messages[len(req.Messages)-1].Content), &info))
			id = info.ID
			require.NoError(t, m.TakeControl(terminal.Owner{Session: owner.Session, Worker: "human"}, id, true, false))
			return call("terminal", map[string]any{"operation": "input", "id": id, "data": "must-not-arrive\n", "control_revision": 2, "sequence": 1}), nil
		case 3:
			require.Contains(t, req.Messages[len(req.Messages)-1].Content, "human has control")
			require.NoError(t, m.TakeControl(terminal.Owner{Session: owner.Session, Worker: "human"}, id, true, true))
			return call("terminal", map[string]any{"operation": "input", "id": id, "data": "accepted\n", "control_revision": 3, "sequence": 1}), nil
		case 4:
			// The fixture provider returns instantly; wait for the real shell
			// instead of burning model steps rereading the initial echo.
			require.Eventually(t, func() bool {
				chunk, err := m.Read(ctx, owner, id, false, 0, 4096, 100*time.Millisecond)
				return err == nil && strings.Contains(chunk.Output, "RESUMED:accepted")
			}, 2*time.Second, 10*time.Millisecond)
			return call("terminal", map[string]any{"operation": "read", "id": id, "wait_ms": 200}), nil
		default:
			last := req.Messages[len(req.Messages)-1].Content
			if step > 4 && strings.Contains(last, "RESUMED:accepted") {
				require.NotContains(t, last, "must-not-arrive")
				return providers.Response{Content: "Resumed and verified actual terminal output."}, nil
			}
			return call("terminal", map[string]any{"operation": "read", "id": id, "wait_ms": 200}), nil
		}
	}}
	f := codingFactory(dir, p)
	f.Terminals = m
	f.TerminalSession = owner.Session
	w, err := f.Build(subagent.RealWorkerConfig{WorkerID: owner.Worker, Goal: "Use terminal and continue after human returns control", Tools: []string{"terminal"}, MaxSteps: 8})
	require.NoError(t, err)
	result, err := w.Run(t.Context())
	require.NoError(t, err)
	require.Equal(t, "completed", result.Status)
	require.False(t, result.ToolTrace[1].Success)
	require.Contains(t, result.ToolTrace[1].Error, "human has control")
	require.True(t, result.ToolTrace[2].Success)
}

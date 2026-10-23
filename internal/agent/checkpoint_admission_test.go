package agent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
	"github.com/stretchr/testify/require"
)

func TestRequiredCheckpointFailureDeniesActualWriteRunAndStream(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(fmt.Sprint("stream=", streaming), func(t *testing.T) {
			workspace := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(workspace, "source.txt"), []byte("original"), 0600))
			registry := tools.NewRegistry()
			require.NoError(t, registry.Register(tools.NewFSTool(workspace)))
			provider := &mockProvider{responses: []providers.Response{
				{ToolCalls: []providers.ToolCall{{ID: "guarded-write", Name: "fs", Arguments: `{"action":"write","path":"source.txt","content":"lost original"}`}}},
				{Content: "Checkpoint failed; write was refused."},
			}}
			a := New(Config{Provider: provider, Tools: registry, Model: "fixture", MaxTokens: 4096, MaxSteps: 4, SessionID: "required-checkpoint"})
			snap := &fakeSnapshotter{err: errors.New("private storage unavailable")}
			a.SetCheckpointSnapshotter(snap)
			if streaming {
				events, err := a.Stream(t.Context(), "Update source.txt")
				require.NoError(t, err)
				for event := range events {
					require.NotEqual(t, EventError, event.Type, "%v", event.Error)
				}
			} else {
				_, err := a.Run(t.Context(), "Update source.txt")
				require.NoError(t, err)
			}
			body, err := os.ReadFile(filepath.Join(workspace, "source.txt"))
			require.NoError(t, err)
			require.Equal(t, "original", string(body))
			require.Len(t, snap.calls, 1)
			found := false
			for _, message := range a.History() {
				if message.Role == "tool" && message.ToolCallID == "guarded-write" {
					require.Contains(t, message.Content, "not executed: required checkpoint failed")
					found = true
				}
			}
			require.True(t, found)
		})
	}
}

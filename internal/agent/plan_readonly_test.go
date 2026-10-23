package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/security"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
	"github.com/stretchr/testify/require"
)

func TestPlanModeDeniesActualModelWritesInRunAndStream(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(fmt.Sprint("stream=", streaming), func(t *testing.T) {
			workspace := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(workspace, "source.txt"), []byte("original"), 0600))
			registry := tools.NewRegistry()
			require.NoError(t, registry.Register(tools.NewFSTool(workspace)))
			provider := &mockProvider{responses: []providers.Response{
				{ToolCalls: []providers.ToolCall{{ID: "denied-write", Name: "fs", Arguments: `{"action":"write","path":"source.txt","content":"changed"}`}}},
				{ToolCalls: []providers.ToolCall{{ID: "allowed-read", Name: "fs", Arguments: `{"action":"read","path":"source.txt"}`}}},
				{Content: "Read-only planning complete."},
			}}
			a := New(Config{Provider: provider, Tools: registry, Model: "fixture", MaxTokens: 4096, MaxSteps: 5, SessionID: "planning"})
			a.SetPrivilegeGate(security.NewPrivilegeGate(security.ModeWriter))
			_, err := a.Run(t.Context(), "/plan")
			require.NoError(t, err)
			// A previously installed writer gate or cached tool approval cannot
			// weaken the planning restriction.
			a.SetPrivilegeMode(security.ModeWriter)
			a.allowedTools = map[string]bool{"fs": true}
			require.Equal(t, security.ModeReader, a.PrivilegeMode())
			if streaming {
				events, err := a.Stream(context.Background(), "Review source.txt")
				require.NoError(t, err)
				for event := range events {
					require.NotEqual(t, EventError, event.Type, "%v", event.Error)
				}
			} else {
				_, err = a.Run(t.Context(), "Review source.txt")
				require.NoError(t, err)
			}
			body, err := os.ReadFile(filepath.Join(workspace, "source.txt"))
			require.NoError(t, err)
			require.Equal(t, "original", string(body))
			history, err := json.Marshal(a.History())
			require.NoError(t, err)
			require.Contains(t, string(history), "read-only mode blocks tool")
			require.Contains(t, string(history), "original")
			// Explicit user mode change restores normal writes without changing
			// the independent trusted configuration ceiling.
			_, _ = a.handleSlashCommand(&SlashCommand{Command: "safe"})
			_, err = a.executeTool(t.Context(), "fs", json.RawMessage(`{"action":"write","path":"source.txt","content":"approved"}`))
			require.NoError(t, err)
			body, err = os.ReadFile(filepath.Join(workspace, "source.txt"))
			require.NoError(t, err)
			require.Equal(t, "approved", string(body))
		})
	}
}

func TestInteractiveModesCannotClearConfiguredReadonly(t *testing.T) {
	for _, command := range []string{"safe", "plan", "build", "auto", "yolo"} {
		t.Run(command, func(t *testing.T) {
			workspace := t.TempDir()
			registry := tools.NewRegistry()
			require.NoError(t, registry.Register(tools.NewFSTool(workspace)))
			a := &Agent{toolRegistry: registry}
			a.SetAutoMode("readonly")
			_, handled := a.handleSlashCommand(&SlashCommand{Command: command})
			require.True(t, handled)
			require.Equal(t, security.ModeReader, a.PrivilegeMode())
			_, err := a.executeTool(t.Context(), "fs", json.RawMessage(`{"action":"write","path":"proof.txt","content":"forbidden"}`))
			require.ErrorIs(t, err, security.ErrWriteDenied)
			_, err = os.Stat(filepath.Join(workspace, "proof.txt"))
			require.True(t, os.IsNotExist(err))
		})
	}
}

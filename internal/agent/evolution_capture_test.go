package agent

import (
	"encoding/json"
	"github.com/Sahaj-Tech-ltd/overkill/internal/learning"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"testing"
)

func TestEvolutionCaptureAttributesParallelAndWrappedToolFailures(t *testing.T) {
	base := t.TempDir()
	records := filepath.Join(base, "records")
	engine := learning.NewEngineWithOptions(records, filepath.Join(base, "skills"), nil, nil, nil, learning.EngineOptions{Workspace: base, KnownSecrets: []string{"private-capture-secret"}})
	a := New(Config{Provider: &mockProvider{}, Model: "fixture", SessionID: "parallel-errors"})
	defer a.Shutdown()
	a.SetEvolutionEngine(engine)
	a.appendMessage(providers.Message{Role: "user", Content: "private-capture-secret"})
	a.appendMessage(providers.Message{Role: "assistant", ToolCalls: []providers.ToolCall{{ID: "failed-fs", Name: "fs", Arguments: "private-capture-secret"}, {ID: "successful-fs", Name: "fs"}, {ID: "failed-lsp", Name: "lsp_hover"}}})
	a.appendToolResultMessage("failed-fs", "fs", nil, assertCaptureError{})
	a.appendToolResultMessage("successful-fs", "fs", json.RawMessage(`{"result":"private-capture-secret"}`), nil)
	a.appendToolResultMessage("failed-lsp", "lsp_hover", nil, assertCaptureError{})
	a.appendMessage(providers.Message{Role: "assistant", Content: "Observed failures; no success claim."})
	a.fireAfterTurn(t.Context())
	raw, err := os.ReadFile(filepath.Join(records, "turns.jsonl"))
	require.NoError(t, err)
	require.NotContains(t, string(raw), "private-capture-secret")
	var record learning.LearningRecord
	require.NoError(t, json.Unmarshal(raw, &record))
	require.Len(t, record.ToolExecutions, 3)
	require.Equal(t, "tool execution failed", record.ToolExecutions[0].Error)
	require.Empty(t, record.ToolExecutions[1].Error)
	require.Equal(t, "tool execution failed", record.ToolExecutions[2].Error)
	for _, execution := range record.ToolExecutions {
		require.Empty(t, execution.Args)
		require.Empty(t, execution.Result)
	}
}

type assertCaptureError struct{}

func (assertCaptureError) Error() string { return "private-capture-secret" }

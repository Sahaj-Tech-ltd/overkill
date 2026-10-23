package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCanvasToolHasNoModelSelectedIdentityAndStrictInput(t *testing.T) {
	var calls []string
	tool := NewCanvasTool(func(_ context.Context, action, line string) (json.RawMessage, error) {
		calls = append(calls, action+":"+line)
		return json.RawMessage(`{"state":{"sessionId":"application-bound"}}`), nil
	})
	for _, input := range []string{`{"action":"state"}`, `{"action":"reset"}`, `{"action":"apply","jsonl":"{}"}`} {
		_, err := tool.Execute(t.Context(), json.RawMessage(input))
		require.NoError(t, err)
	}
	require.Len(t, calls, 3)
	for _, input := range []string{`null`, `[]`, `{"action":"state","session_id":"other"}`, `{"action":"reset","path":"src"}`, `{"action":"state","url":"https://evil.invalid"}`, `{"action":"state","Action":"reset"}`, `{"action":"apply","action":"state","jsonl":"{}"}`, `{"action":"state","jsonl":"{}"}`, `{"action":"apply","jsonl":null}`, `{"action":"apply"}`, `{"action":"state"} {}`, `{"action":42}`} {
		_, err := tool.Execute(t.Context(), json.RawMessage(input))
		require.Error(t, err, input)
	}
	require.Len(t, calls, 3)
	require.True(t, tool.IsConcurrencySafe(json.RawMessage(`{"action":"state"}`)))
	require.False(t, tool.IsConcurrencySafe(json.RawMessage(`{"action":"reset"}`)))
	empty := NewDefaultRegistry(FactoryDeps{})
	_, err := empty.Get("canvas")
	require.Error(t, err)
	bound := NewDefaultRegistry(FactoryDeps{Canvas: tool.handler})
	registered, err := bound.Get("canvas")
	require.NoError(t, err)
	require.NotNil(t, registered)
	var schema map[string]any
	require.NoError(t, json.Unmarshal(tool.Schema(), &schema))
	properties := schema["properties"].(map[string]any)
	require.Len(t, properties, 2)
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = tool.Execute(cancelled, json.RawMessage(`{"action":"state"}`))
	require.ErrorIs(t, err, context.Canceled)
	require.Len(t, calls, 3)
}

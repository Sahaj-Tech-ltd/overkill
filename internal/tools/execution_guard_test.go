package tools

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

type guardedFixture struct {
	name  string
	calls atomic.Int32
}

func (t *guardedFixture) Name() string { return t.name }
func (t *guardedFixture) Execute(context.Context, json.RawMessage) (json.RawMessage, error) {
	t.calls.Add(1)
	return json.RawMessage(`{"effect":true}`), nil
}

type guardedFixtureMetadata struct{ *guardedFixture }

func (*guardedFixtureMetadata) Description() string                    { return "fixture contract" }
func (*guardedFixtureMetadata) Schema() json.RawMessage                { return json.RawMessage(`{"type":"object"}`) }
func (*guardedFixtureMetadata) IsConcurrencySafe(json.RawMessage) bool { return true }
func (*guardedFixtureMetadata) InterruptBehavior() InterruptBehavior   { return InterruptCancel }

func TestRegistryGuardCoversLookupConcurrentAndLaterRegistration(t *testing.T) {
	registry := NewRegistry()
	base := &guardedFixture{name: "native"}
	metadata := &guardedFixtureMetadata{&guardedFixture{name: "dynamic"}}
	require.NoError(t, registry.Register(base))
	blocked := errors.New("ownership denied")
	var admitted atomic.Bool
	registry.SetExecutionGuard(func(ctx context.Context, tool Tool, input json.RawMessage) (json.RawMessage, error) {
		if !admitted.Load() {
			return nil, blocked
		}
		return tool.Execute(ctx, input)
	})
	// Dynamic adapters registered after publication must not escape admission.
	require.NoError(t, registry.Register(metadata))
	plain, err := registry.Get("native")
	require.NoError(t, err)
	_, described := plain.(toolDescription)
	require.False(t, described)
	_, schemed := plain.(toolSchema)
	require.False(t, schemed)
	for _, name := range registry.List() {
		tool, err := registry.Get(name)
		require.NoError(t, err)
		_, err = tool.Execute(t.Context(), json.RawMessage(`{}`))
		require.ErrorIs(t, err, blocked)
		tool, concurrent := registry.GetConcurrency(name, json.RawMessage(`{}`))
		require.Equal(t, name == "dynamic", concurrent)
		_, err = tool.Execute(t.Context(), json.RawMessage(`{}`))
		require.ErrorIs(t, err, blocked)
	}
	require.Zero(t, base.calls.Load())
	require.Zero(t, metadata.calls.Load())
	tool, err := registry.Get("dynamic")
	require.NoError(t, err)
	require.Equal(t, "fixture contract", tool.(toolDescription).Description())
	require.JSONEq(t, `{"type":"object"}`, string(tool.(toolSchema).Schema()))
	require.Equal(t, InterruptCancel, tool.(InterruptibleTool).InterruptBehavior())
	admitted.Store(true)
	_, err = tool.Execute(t.Context(), json.RawMessage(`{}`))
	require.NoError(t, err)
	require.Equal(t, int32(1), metadata.calls.Load())
}

package main

import (
	"encoding/json"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestCanvasToolRuntimeBindsOnlyPersistedCanonicalSession(t *testing.T) {
	database, _ := testpg.Open(t)
	sessions, err := session.NewPostgresStore(database)
	require.NoError(t, err)
	services, err := newRuntimeServices(config.Default(), database, t.TempDir())
	require.NoError(t, err)
	a, b := session.NewSession(t.TempDir()), session.NewSession(t.TempDir())
	require.NoError(t, sessions.Create(t.Context(), a))
	require.NoError(t, sessions.Create(t.Context(), b))
	first, err := services.BuildSession(t.Context(), a)
	require.NoError(t, err)
	defer first.Cleanup()
	second, err := services.BuildSession(t.Context(), b)
	require.NoError(t, err)
	defer second.Cleanup()
	tool, err := first.Tools.Get("canvas")
	require.NoError(t, err)
	boundID := a.ID
	a.ID = b.ID // Mutable caller metadata cannot retarget the constructed tool.
	boundStore := services.Canvas
	services.Canvas = nil // The callback captures its store, not this mutable field.
	output, err := tool.Execute(t.Context(), json.RawMessage(`{"action":"apply","jsonl":"{\"createSurface\":{\"surfaceId\":\"main\",\"catalog\":\"overkill.canvas.v1\"}}"}`))
	require.NoError(t, err)
	require.Contains(t, string(output), boundID)
	a.ID = boundID
	services.Canvas = boundStore
	separate, err := services.Canvas.Get(t.Context(), b.ID)
	require.NoError(t, err)
	require.Empty(t, separate.Surfaces)
	_, err = tool.Execute(t.Context(), json.RawMessage(`{"action":"reset","session_id":"`+b.ID+`"}`))
	require.Error(t, err)
	_, err = tool.Execute(t.Context(), json.RawMessage(`{"action":"reset"}`))
	require.NoError(t, err)
	cleared, err := services.Canvas.Get(t.Context(), a.ID)
	require.NoError(t, err)
	require.Empty(t, cleared.Surfaces)
	require.EqualValues(t, 2, cleared.Revision)
	forgedLine, _ := json.Marshal(map[string]any{"createSurface": map[string]any{"surfaceId": "foreign", "catalog": "overkill.canvas.v1", "sessionId": b.ID}})
	forgedInput, _ := json.Marshal(map[string]string{"action": "apply", "jsonl": string(forgedLine)})
	_, err = tool.Execute(t.Context(), forgedInput)
	require.Error(t, err)
	stillCleared, err := services.Canvas.Get(t.Context(), a.ID)
	require.NoError(t, err)
	require.Equal(t, cleared, stillCleared)
	unpersisted, err := services.BuildSession(t.Context(), session.NewSession(t.TempDir()))
	require.NoError(t, err)
	defer unpersisted.Cleanup()
	_, err = unpersisted.Tools.Get("canvas")
	require.Error(t, err)
}

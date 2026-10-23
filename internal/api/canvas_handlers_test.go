package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/Sahaj-Tech-ltd/overkill/internal/web"
	"github.com/stretchr/testify/require"
)

func TestCanvasApplicationRPCUsesCommittedSessionStore(t *testing.T) {
	database, _ := testpg.Open(t)
	sessions, err := session.NewPostgresStore(database)
	require.NoError(t, err)
	first, second := session.NewSession(t.TempDir()), session.NewSession(t.TempDir())
	require.NoError(t, sessions.Create(t.Context(), first))
	require.NoError(t, sessions.Create(t.Context(), second))
	store := web.NewCanvasStore(database)
	require.NoError(t, store.Migrate(t.Context()))
	server := NewServer(ServerConfig{Config: config.Default(), SessionStore: sessions, Canvas: store})
	startHTTPServer(t, server)
	mutate := func(sid, action string, payload map[string]any) Response {
		t.Helper()
		payload["surfaceId"] = "main"
		raw, err := json.Marshal(map[string]any{action: payload})
		require.NoError(t, err)
		response := sidebarRPC(t, server, "canvas.apply", map[string]string{"session_id": sid, "jsonl": string(raw)})
		require.Nil(t, response.Error)
		return response
	}
	mutate(first.ID, "createSurface", map[string]any{"catalog": web.CatalogCanvasV1})
	mutate(first.ID, "surfaceUpdate", map[string]any{"components": []any{map[string]any{"id": "root", "type": "Text", "textPath": "/status"}}})
	mutate(first.ID, "dataModelUpdate", map[string]any{"contents": map[string]any{"status": "Ready"}})
	mutate(first.ID, "beginRendering", map[string]any{"root": "root"})
	read := sidebarRPC(t, server, "canvas.state", map[string]string{"session_id": first.ID})
	require.Nil(t, read.Error)
	raw, err := json.Marshal(read.Result)
	require.NoError(t, err)
	var result struct {
		State web.CanvasState `json:"state"`
	}
	require.NoError(t, json.Unmarshal(raw, &result))
	require.EqualValues(t, 4, result.State.Revision)
	require.Equal(t, "Ready", result.State.Surfaces[0].Data["status"])
	// Committed reads remain available during an admitted turn; mutations do not.
	release, err := server.beginSessionOperation(first.ID)
	require.NoError(t, err)
	require.Nil(t, sidebarRPC(t, server, "canvas.state", map[string]string{"session_id": first.ID}).Error)
	require.NotNil(t, sidebarRPC(t, server, "canvas.reset", map[string]string{"session_id": first.ID}).Error)
	release()
	require.Empty(t, server.agents, "canvas reads must not create an agent")
	before, err := store.Get(t.Context(), first.ID)
	require.NoError(t, err)
	require.NotNil(t, sidebarRPC(t, server, "canvas.apply", map[string]string{"session_id": first.ID, "jsonl": `{"surfaceUpdate":{"surfaceId":"main","components":[{"id":"root","type":"Script"}]}}`}).Error)
	after, err := store.Get(t.Context(), first.ID)
	require.NoError(t, err)
	require.Equal(t, before, after)
	isolated, err := store.Get(t.Context(), second.ID)
	require.NoError(t, err)
	require.Empty(t, isolated.Surfaces)
	restored, err := web.NewCanvasStore(database).Get(t.Context(), first.ID)
	require.NoError(t, err)
	require.Equal(t, before, restored)
	for _, params := range []string{`null`, `{"session_id":null}`, `{"session_id":"` + first.ID + `","session_id":"` + second.ID + `"}`, `{"session_id":"` + first.ID + `","Session_id":"x"}`, `{"session_id":"` + first.ID + `"} {}`, `{"session_id":"` + first.ID + `","jsonl":"unexpected"}`} {
		_, rpcErr := server.handleCanvas(t.Context(), "canvas.state", []byte(params))
		require.NotNil(t, rpcErr, params)
	}
	require.NotNil(t, sidebarRPC(t, server, "canvas.state", map[string]string{"session_id": "nonexistent"}).Error)
	request, err := http.NewRequest("POST", server.Addr()+"/rpc", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"canvas.state","params":{"session_id":"`+first.ID+`"}}`))
	require.NoError(t, err)
	denied, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	denied.Body.Close()
	require.Equal(t, 401, denied.StatusCode)
	require.Nil(t, sidebarRPC(t, server, "canvas.reset", map[string]string{"session_id": first.ID}).Error)
	reset, err := store.Get(t.Context(), first.ID)
	require.NoError(t, err)
	require.EqualValues(t, 5, reset.Revision)
	require.Empty(t, reset.Surfaces)
	require.NoError(t, sessions.Delete(context.Background(), first.ID))
	_, err = store.Get(t.Context(), first.ID)
	require.ErrorIs(t, err, web.ErrCanvasSessionNotFound)
}

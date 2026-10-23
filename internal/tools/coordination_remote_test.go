package tools

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/acp"
	"github.com/Sahaj-Tech-ltd/overkill/internal/coordination"
	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/stretchr/testify/require"
)

func TestRemoteCoordinationSixAgentToolsUseActualScopedACP(t *testing.T) {
	db, _ := testpg.Open(t)
	store, err := coordination.NewStore(t.Context(), db)
	require.NoError(t, err)
	_, frontToken, err := store.RegisterPeer(t.Context(), coordination.Peer{ProjectID: "p", Name: "frontend", Role: "UI", Workspace: t.TempDir()})
	require.NoError(t, err)
	_, backToken, err := store.RegisterPeer(t.Context(), coordination.Peer{ProjectID: "p", Name: "backend", Role: "API", Workspace: t.TempDir()})
	require.NoError(t, err)
	server := acp.NewServer(acp.Config{Token: "global", Coordination: store})
	defer server.Shutdown(context.Background())
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	makeTools := func(token string) map[string]Tool {
		list, err := NewRemoteCoordinationTools(httpServer.URL, token)
		require.NoError(t, err)
		require.Len(t, list, 6)
		result := map[string]Tool{}
		for _, base := range list {
			tool := base.(*remoteCoordinationTool)
			require.NotContains(t, string(tool.Schema()), token)
			require.NotContains(t, tool.Description(), token)
			require.NotContains(t, string(tool.Schema()), httpServer.URL)
			result[tool.Name()] = tool
		}
		return result
	}
	front, back := makeTools(frontToken), makeTools(backToken)
	send := json.RawMessage(`{"to":"backend","kind":"request","payload":{"question":"GET /users?"},"idempotency_key":"ask"}`)
	output, err := front["agent_send"].Execute(t.Context(), send)
	require.NoError(t, err)
	var receipt acp.CoordinationResponse
	require.NoError(t, json.Unmarshal(output, &receipt))
	id := receipt.Message.ID
	output, err = back["agent_inbox"].Execute(t.Context(), json.RawMessage(`{}`))
	require.NoError(t, err)
	var inbox acp.CoordinationResponse
	require.NoError(t, json.Unmarshal(output, &inbox))
	require.Len(t, inbox.Messages, 1)
	require.Equal(t, "backend", inbox.Self.Name)
	require.Len(t, inbox.Peers, 2)
	_, err = acp.NewClient(httpServer.URL, backToken).Coordination(t.Context(), "claim", acp.CoordinationRequest{})
	require.NoError(t, err)
	body, _ := json.Marshal(map[string]any{"message_id": id, "payload": map[string]any{"schema": "users[]"}, "idempotency_key": "reply"})
	output, err = back["agent_reply"].Execute(t.Context(), body)
	require.NoError(t, err)
	var answer acp.CoordinationResponse
	require.NoError(t, json.Unmarshal(output, &answer))
	require.Equal(t, id, answer.Message.InReplyTo)
	require.Equal(t, receipt.Message.ConversationID, answer.Message.ConversationID)
	body, _ = json.Marshal(map[string]any{"message_id": id})
	output, err = front["agent_task_status"].Execute(t.Context(), body)
	require.NoError(t, err)
	require.Contains(t, string(output), `"completed"`)
	output, err = front["agent_await"].Execute(t.Context(), body)
	require.NoError(t, err)
	require.Contains(t, string(output), `"completed"`)
	send = json.RawMessage(`{"to":"backend","kind":"request","payload":{},"idempotency_key":"cancel-me"}`)
	output, err = front["agent_send"].Execute(t.Context(), send)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(output, &receipt))
	body, _ = json.Marshal(map[string]any{"message_id": receipt.Message.ID})
	output, err = front["agent_cancel"].Execute(t.Context(), body)
	require.NoError(t, err)
	require.Contains(t, string(output), `"cancelled"`)
	for _, forged := range []json.RawMessage{json.RawMessage(`{"from":"backend"}`), json.RawMessage(`{"token":"spoof"}`), json.RawMessage(`{"endpoint":"http://elsewhere"}`)} {
		_, err = front["agent_send"].Execute(t.Context(), forged)
		require.Error(t, err)
		require.NotContains(t, err.Error(), frontToken)
	}
}

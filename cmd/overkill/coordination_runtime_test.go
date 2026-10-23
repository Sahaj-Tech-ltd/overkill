package main

import (
	"encoding/json"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/coordination"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/stretchr/testify/require"
)

func TestSessionRuntimeBindsCoordinationIdentityAndDurableReplies(t *testing.T) {
	database, _ := testpg.Open(t)
	store, err := session.NewPostgresStore(database)
	require.NoError(t, err)
	services, err := newRuntimeServices(config.Default(), database, t.TempDir())
	require.NoError(t, err)
	frontSession, backSession := session.NewSession(t.TempDir()), session.NewSession(t.TempDir())
	for _, sess := range []*session.Session{frontSession, backSession} {
		require.NoError(t, store.Create(t.Context(), sess))
	}
	frontend, _, err := services.Coordination.RegisterPeer(t.Context(), coordination.Peer{ProjectID: "project", Name: "frontend", Role: "UI", Workspace: frontSession.Folder})
	require.NoError(t, err)
	backend, _, err := services.Coordination.RegisterPeer(t.Context(), coordination.Peer{ProjectID: "project", Name: "backend", Role: "API", Workspace: backSession.Folder})
	require.NoError(t, err)
	require.NoError(t, services.Coordination.BindSession(t.Context(), frontend.ID, frontSession.ID))
	require.NoError(t, services.Coordination.BindSession(t.Context(), backend.ID, backSession.ID))
	require.NoError(t, services.Coordination.BindSession(t.Context(), frontend.ID, frontSession.ID), "binding retry must be idempotent")
	require.Error(t, services.Coordination.BindSession(t.Context(), frontend.ID, backSession.ID), "peer cannot impersonate another workspace")
	frontRuntime, err := services.BuildSession(t.Context(), frontSession)
	require.NoError(t, err)
	defer frontRuntime.Cleanup()
	backRuntime, err := services.BuildSession(t.Context(), backSession)
	require.NoError(t, err)
	defer backRuntime.Cleanup()
	output, err := runtimeTool(frontRuntime.Tools, "agent_send", map[string]any{"to": "backend", "kind": "request", "payload": map[string]string{"question": "GET /users schema?"}, "idempotency_key": "api-question"})
	require.NoError(t, err)
	var receipt struct {
		Message coordination.Message `json:"message"`
	}
	require.NoError(t, json.Unmarshal(output, &receipt))
	require.Equal(t, frontend.ID, receipt.Message.From)
	require.Equal(t, backend.ID, receipt.Message.To)
	_, err = runtimeTool(backRuntime.Tools, "agent_reply", map[string]any{"message_id": receipt.Message.ID, "payload": map[string]string{"schema": "users[]"}, "idempotency_key": "schema-answer"})
	require.NoError(t, err)
	// A new runtime uses the persisted peer binding and the durable inbox.
	frontRuntime.Cleanup()
	restored, err := services.BuildSession(t.Context(), frontSession)
	require.NoError(t, err)
	defer restored.Cleanup()
	output, err = runtimeTool(restored.Tools, "agent_inbox", map[string]any{})
	require.NoError(t, err)
	require.Contains(t, string(output), "users[]")
	require.Contains(t, string(output), receipt.Message.ConversationID)
}

package tools

import (
	"context"
	"encoding/json"
	"github.com/Sahaj-Tech-ltd/overkill/internal/coordination"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"testing"
)

type opaqueReadNamedTool struct{}

func (opaqueReadNamedTool) Name() string { return "acp_status" }
func (opaqueReadNamedTool) Execute(context.Context, json.RawMessage) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}

func TestCoordinationGuardAllowsTypedCanvasACPReadsButFencesEffects(t *testing.T) {
	database, _ := testpg.Open(t)
	sessions, err := session.NewPostgresStore(database)
	require.NoError(t, err)
	store, err := coordination.NewStore(t.Context(), database)
	require.NoError(t, err)
	folder := t.TempDir()
	var peers []coordination.Peer
	for _, name := range []string{"frontend", "backend"} {
		metadata := session.NewSession(folder)
		require.NoError(t, sessions.Create(t.Context(), metadata))
		peer, err := store.RegisterSessionPeer(t.Context(), metadata.ID, name, "fixture")
		require.NoError(t, err)
		peers = append(peers, peer)
	}
	_, err = store.CreateContract(t.Context(), peers[0].ID, coordination.ContractSpec{Name: "Client", Endpoint: "GET /client", SchemaVersion: "1", Schema: json.RawMessage(`{}`), AcceptanceCriteria: []string{"works"}, Workspace: folder, OwnedPaths: []string{"frontend"}})
	require.NoError(t, err)
	guard := NewCoordinationExecutionGuard(store, peers[1].ID, folder)
	reads, writes := 0, 0
	canvas := NewCanvasTool(func(_ context.Context, action, line string) (json.RawMessage, error) {
		if action == "state" {
			reads++
		} else {
			writes++
		}
		return json.RawMessage(`{}`), nil
	})
	_, err = guard(t.Context(), canvas, json.RawMessage(`{"action":"state"}`))
	require.NoError(t, err)
	require.Equal(t, 1, reads)
	for _, input := range []string{`{"action":"reset"}`, `{"action":"apply","jsonl":"{}"}`} {
		_, err = guard(t.Context(), canvas, json.RawMessage(input))
		require.ErrorIs(t, err, coordination.ErrOwnershipConflict)
	}
	require.Zero(t, writes)
	requests := 0
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "Bearer fixture", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/sessions" {
			w.Write([]byte(`[]`))
			return
		}
		w.Write([]byte(`{"taskId":"11111111-1111-4111-8111-111111111111","messageId":"22222222-2222-4222-8222-222222222222","sessionId":"remote-session","status":"completed"}`))
	}))
	defer remote.Close()
	for _, tool := range NewACPTaskTools(map[string]ACPRemote{"peer": {URL: remote.URL, Token: "fixture"}}) {
		input := json.RawMessage(`{"peer":"peer","task_id":"11111111-1111-4111-8111-111111111111"}`)
		switch tool.Name() {
		case "acp_sessions":
			input = json.RawMessage(`{"peer":"peer"}`)
		case "acp_await":
			input = json.RawMessage(`{"peer":"peer","task_id":"11111111-1111-4111-8111-111111111111","wait_ms":0}`)
		}
		_, err := guard(t.Context(), tool, input)
		if tool.Name() == "acp_submit" || tool.Name() == "acp_cancel" {
			require.ErrorIs(t, err, coordination.ErrOwnershipConflict)
		} else {
			require.NoError(t, err, tool.Name())
		}
	}
	require.Equal(t, 3, requests)
	_, err = guard(t.Context(), opaqueReadNamedTool{}, json.RawMessage(`{}`))
	require.ErrorIs(t, err, coordination.ErrOwnershipConflict, "names alone cannot exempt an arbitrary adapter")
}

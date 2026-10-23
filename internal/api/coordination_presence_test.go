package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/coordination"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/stretchr/testify/require"
)

func TestCoordinationRuntimePresenceActualTurnAndLifecycle(t *testing.T) {
	db, _ := testpg.Open(t)
	sessions, err := session.NewPostgresStore(db)
	require.NoError(t, err)
	store, err := coordination.NewStore(t.Context(), db)
	require.NoError(t, err)
	started, release := make(chan struct{}), make(chan struct{})
	var startOnce, releaseOnce sync.Once
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startOnce.Do(func() { close(started) })
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ready"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":1}}`))
	}))
	server := NewServer(ServerConfig{Config: coordinationConfig(provider.URL), SessionStore: sessions, CoordStore: store})
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }); server.Close(); provider.Close() })
	folder := t.TempDir()
	front, back := session.NewSession(folder), session.NewSession(folder)
	for i, metadata := range []*session.Session{front, back} {
		require.NoError(t, sessions.Create(t.Context(), metadata))
		_, err = store.RegisterSessionPeer(t.Context(), metadata.ID, []string{"front", "back"}[i], "fixture")
		require.NoError(t, err)
	}
	require.NoError(t, server.StartBackground(t.Context()))
	read := func() []coordination.SessionPeer {
		t.Helper()
		params, _ := json.Marshal(map[string]string{"session_id": front.ID})
		response, rpcErr := server.handleCoordination(t.Context(), "coordination.peers", params)
		require.Nil(t, rpcErr)
		return response.(map[string]any)["peers"].([]coordination.SessionPeer)
	}
	state := func(id string) coordination.RuntimePresence {
		t.Helper()
		for _, p := range read() {
			if p.SessionID == id {
				return p.Presence
			}
		}
		t.Fatal("peer absent")
		return coordination.RuntimePresence{}
	}
	require.Equal(t, "unknown", state(front.ID).State)
	done := make(chan *RPCError, 1)
	go func() {
		params, _ := json.Marshal(map[string]string{"session_id": front.ID, "message": "work"})
		_, rpcErr := server.handleAgentSend(t.Context(), params)
		done <- rpcErr
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("provider did not enter")
	}
	require.Equal(t, "working", state(front.ID).State)
	require.Equal(t, "unknown", state(back.ID).State)
	server.pendingQuestionMu.Lock()
	server.pendingQuestionData[front.ID] = &agent.Question{}
	server.pendingQuestionMu.Unlock()
	require.Equal(t, "waiting_for_input", state(front.ID).State)
	server.pendingQuestionMu.Lock()
	delete(server.pendingQuestionData, front.ID)
	server.pendingQuestionMu.Unlock()
	releaseOnce.Do(func() { close(release) })
	select {
	case rpcErr := <-done:
		require.Nil(t, rpcErr)
	case <-time.After(5 * time.Second):
		t.Fatal("turn failed to finish")
	}
	require.Equal(t, "ready", state(front.ID).State)
	// The background heartbeat must advance without any client read refreshing it.
	peer, err := store.PeerForSession(t.Context(), front.ID)
	require.NoError(t, err)
	var first time.Time
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT observed_at FROM coordination_runtime_presence WHERE peer_id=$1`, peer.ID).Scan(&first))
	require.Eventually(t, func() bool {
		var next time.Time
		return db.QueryRowContext(t.Context(), `SELECT observed_at FROM coordination_runtime_presence WHERE peer_id=$1`, peer.ID).Scan(&next) == nil && next.After(first)
	}, 8*time.Second, 50*time.Millisecond)
	finish, err := server.beginSessionOperation(front.ID)
	require.NoError(t, err)
	require.Equal(t, "busy", state(front.ID).State)
	finish()
	server.discardSessionRuntime(front.ID)
	require.Equal(t, "unknown", state(front.ID).State)
	// No mutation surface accepts a model/client-provided observer or state.
	for _, field := range []string{"state", "observer_id", "presence"} {
		params, _ := json.Marshal(map[string]string{"session_id": front.ID, field: "working"})
		_, rpcErr := server.handleCoordination(t.Context(), "coordination.peers", params)
		require.NotNil(t, rpcErr)
	}
	_, rpcErr := server.getOrCreateAgent(t.Context(), front.ID)
	require.Nil(t, rpcErr)
	require.Equal(t, "ready", state(front.ID).State)
	server.Close()
	presence, err := store.RuntimePresenceForPeers(t.Context(), []string{peer.ID})
	require.NoError(t, err)
	require.Equal(t, "unknown", presence[peer.ID].State)
}

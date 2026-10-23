package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
	"github.com/stretchr/testify/require"
)

type blockingCommitStore struct {
	*mockSessionStore
	entered chan struct{}
	release chan struct{}
}

func (s *blockingCommitStore) Save(ctx context.Context, metadata *session.Session) error {
	select {
	case s.entered <- struct{}{}:
	default:
	}
	select {
	case <-s.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	return s.mockSessionStore.Save(ctx, metadata)
}

func TestSessionAdmissionCoversPersistenceAndMutations(t *testing.T) {
	metadata := session.NewSession(t.TempDir())
	store := &blockingCommitStore{mockSessionStore: &mockSessionStore{sessions: map[string]*session.Session{metadata.ID: metadata}}, entered: make(chan struct{}, 1), release: make(chan struct{})}
	s := NewServer(ServerConfig{Config: config.Default(), SessionStore: store})
	a := agent.New(agent.Config{Provider: streamingTestProvider{}, Tools: tools.NewRegistry(), Model: "fixture", SessionID: metadata.ID})
	defer a.Shutdown()
	s.agents[metadata.ID] = a
	request, err := json.Marshal(map[string]string{"session_id": metadata.ID, "message": "first turn"})
	require.NoError(t, err)
	done := make(chan *RPCError, 1)
	go func() { _, rpcErr := s.handleAgentSend(context.Background(), request); done <- rpcErr }()
	select {
	case <-store.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("turn did not reach persistence")
	}
	// The provider has finished and Agent.IsRunning is false, but its durable
	// commit still owns admission across RPC, SSE and session mutations.
	require.False(t, a.IsRunning())
	_, rpcErr := s.handleAgentSend(context.Background(), request)
	require.NotNil(t, rpcErr)
	require.Contains(t, rpcErr.Message, "busy")
	id, err := json.Marshal(map[string]string{"id": metadata.ID})
	require.NoError(t, err)
	_, rpcErr = s.handleSessionDelete(context.Background(), id)
	require.NotNil(t, rpcErr)
	require.Contains(t, rpcErr.Message, "busy")
	_, rpcErr = s.handleSessionLoad(context.Background(), id)
	require.NotNil(t, rpcErr)
	require.Contains(t, rpcErr.Message, "busy")
	recorder := httptest.NewRecorder()
	s.handleStream(recorder, httptest.NewRequest("GET", "/stream?session_id="+metadata.ID+"&message=second", nil))
	require.Equal(t, http.StatusConflict, recorder.Code)
	close(store.release)
	select {
	case rpcErr := <-done:
		require.Nil(t, rpcErr)
	case <-time.After(5 * time.Second):
		t.Fatal("turn did not release admission after save")
	}
	release, err := s.beginSessionOperation(metadata.ID)
	require.NoError(t, err)
	release()
	release()
}

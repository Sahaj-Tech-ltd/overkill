package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/coordination"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
	"github.com/stretchr/testify/require"
)

func TestCoordinationResultBoundsEscapedUnicode(t *testing.T) {
	original := strings.Repeat("\"\\\n世界", 40000)
	payload, err := coordinationResult("session", original)
	require.NoError(t, err)
	require.LessOrEqual(t, len(payload), 65536)
	var result struct {
		SessionID, Response string
		Truncated           bool
	}
	require.NoError(t, json.Unmarshal(payload, &result))
	require.True(t, result.Truncated)
	require.NotEmpty(t, result.Response)
	require.True(t, strings.HasPrefix(original, result.Response))
}

func TestCoordinationBackgroundExplicitLifecycle(t *testing.T) {
	s := NewServer(ServerConfig{})
	require.False(t, s.CoordinationStatus()["started"])
	require.NoError(t, s.StartBackground(t.Context()))
	require.NoError(t, s.StartBackground(t.Context()))
	s.Close()
	s.Close()
	require.True(t, s.CoordinationStatus()["closed"])
	require.Error(t, s.StartBackground(t.Context()))
}

func coordinationConfig(base string) *config.Config {
	cfg := config.Default()
	cfg.Agent.DefaultProvider = "fixture"
	cfg.Agent.DefaultModel = "backend"
	cfg.Providers = []config.ProviderConfig{{Name: "fixture", Type: "openai", APIKey: "fixture-only", BaseURL: base}}
	return cfg
}

func coordinationRuntime(t *testing.T) SessionRuntimeFactory {
	return func(_ context.Context, metadata *session.Session) (*SessionRuntime, error) {
		registry := tools.NewRegistry()
		require.NoError(t, registry.Register(tools.NewFSTool(metadata.Folder)))
		return &SessionRuntime{Tools: registry}, nil
	}
}

func TestCoordinationAutomaticDeliveryUsesBoundSessionAndWakesSender(t *testing.T) {
	database, _ := testpg.Open(t)
	sessions, err := session.NewPostgresStore(database)
	require.NoError(t, err)
	store, err := coordination.NewStore(t.Context(), database)
	require.NoError(t, err)
	var backendCalls, frontendCalls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model    string            `json:"model"`
			Messages []json.RawMessage `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if body.Model == "frontend" {
			frontendCalls.Add(1)
			encoded, _ := json.Marshal(body.Messages)
			if !strings.Contains(string(encoded), "backend-ready") {
				t.Error("frontend was not given backend answer")
			}
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"Frontend received backend-ready"},"finish_reason":"stop"}]}`)
		} else if backendCalls.Add(1) == 1 {
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"actual-write","type":"function","function":{"name":"fs","arguments":"{\"action\":\"write\",\"path\":\"endpoint.json\",\"content\":\"{\\\"ready\\\":true}\"}"}}]},"finish_reason":"tool_calls"}]}`)
		} else {
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"backend-ready"},"finish_reason":"stop"}]}`)
		}
	}))
	defer provider.Close()
	var peers []coordination.Peer
	var metadata []*session.Session
	for _, name := range []string{"frontend", "backend"} {
		peer, _, err := store.RegisterPeer(t.Context(), coordination.Peer{ProjectID: "fixture", Name: name, Role: name, Workspace: t.TempDir()})
		require.NoError(t, err)
		sess := session.NewSession(peer.Workspace)
		sess.Provider = "fixture"
		sess.Model = name
		require.NoError(t, sessions.Create(t.Context(), sess))
		require.NoError(t, store.BindSession(t.Context(), peer.ID, sess.ID))
		peers = append(peers, peer)
		metadata = append(metadata, sess)
	}
	server := NewServer(ServerConfig{Config: coordinationConfig(provider.URL), SessionStore: sessions, CoordStore: store, SessionRuntimeFactory: coordinationRuntime(t)})
	release, err := server.beginSessionOperation(metadata[1].ID)
	require.NoError(t, err)
	defer release()
	request, err := store.Send(t.Context(), peers[0].ID, coordination.SendInput{To: "backend", Kind: "request", Payload: json.RawMessage(`{"task":"create endpoint"}`), IdempotencyKey: "create"})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- server.Start(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(8 * time.Second):
			t.Error("HTTP service failed to stop")
		}
	})
	select {
	case <-server.Ready():
	case <-time.After(5 * time.Second):
		t.Fatal("HTTP service not ready")
	}
	time.Sleep(600 * time.Millisecond)
	queued, err := store.Read(t.Context(), peers[1].ID, request.ID)
	require.NoError(t, err)
	require.Equal(t, coordination.Accepted, queued.State)
	require.Zero(t, backendCalls.Load(), "busy session must not execute queued work")
	release()
	require.Eventually(t, func() bool { return frontendCalls.Load() == 1 }, 8*time.Second, 20*time.Millisecond)
	require.Eventually(t, func() bool {
		loaded, err := sessions.Load(t.Context(), metadata[0].ID)
		if err != nil {
			return false
		}
		encoded, _ := json.Marshal(loaded.Messages)
		return strings.Contains(string(encoded), "Frontend received backend-ready")
	}, 5*time.Second, 20*time.Millisecond)
	actual, err := os.ReadFile(filepath.Join(metadata[1].Folder, "endpoint.json"))
	require.NoError(t, err)
	require.JSONEq(t, `{"ready":true}`, string(actual))
	_, err = os.Stat(filepath.Join(metadata[0].Folder, "endpoint.json"))
	require.True(t, os.IsNotExist(err))
	loaded, err := sessions.Load(t.Context(), metadata[1].ID)
	require.NoError(t, err)
	encoded, _ := json.Marshal(loaded.Messages)
	require.Contains(t, string(encoded), "actual-write")
	require.Contains(t, string(encoded), "backend-ready")
	server.mu.RLock()
	cached := server.agents[metadata[1].ID]
	server.mu.RUnlock()
	same, rpcErr := server.getOrCreateAgent(t.Context(), metadata[1].ID)
	require.Nil(t, rpcErr)
	require.Same(t, cached, same)
	current, err := store.Read(t.Context(), peers[1].ID, request.ID)
	require.NoError(t, err)
	require.Equal(t, coordination.Completed, current.State)
}

func TestCoordinationShutdownBlocksWithoutReplay(t *testing.T) {
	database, _ := testpg.Open(t)
	sessions, err := session.NewPostgresStore(database)
	require.NoError(t, err)
	store, err := coordination.NewStore(t.Context(), database)
	require.NoError(t, err)
	var calls atomic.Int32
	started := make(chan struct{}, 1)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		calls.Add(1)
		select {
		case started <- struct{}{}:
		default:
		}
		<-r.Context().Done()
	}))
	defer provider.Close()
	front, _, err := store.RegisterPeer(t.Context(), coordination.Peer{ProjectID: "fixture", Name: "frontend", Role: "frontend", Workspace: t.TempDir()})
	require.NoError(t, err)
	back, _, err := store.RegisterPeer(t.Context(), coordination.Peer{ProjectID: "fixture", Name: "backend", Role: "backend", Workspace: t.TempDir()})
	require.NoError(t, err)
	metadata := session.NewSession(back.Workspace)
	metadata.Provider = "fixture"
	metadata.Model = "backend"
	require.NoError(t, sessions.Create(t.Context(), metadata))
	require.NoError(t, store.BindSession(t.Context(), back.ID, metadata.ID))
	message, err := store.Send(t.Context(), front.ID, coordination.SendInput{To: "backend", Kind: "request", Payload: json.RawMessage(`{"task":"wait"}`), IdempotencyKey: "wait"})
	require.NoError(t, err)
	sc := ServerConfig{Config: coordinationConfig(provider.URL), SessionStore: sessions, CoordStore: store, SessionRuntimeFactory: coordinationRuntime(t)}
	server := NewServer(sc)
	defer server.Close()
	require.NoError(t, server.StartBackground(t.Context()))
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not start")
	}
	closed := make(chan struct{})
	go func() { server.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(8 * time.Second):
		t.Fatal("shutdown did not cancel model")
	}
	current, err := store.Read(t.Context(), back.ID, message.ID)
	require.NoError(t, err)
	require.Equal(t, coordination.Blocked, current.State)
	loaded, err := sessions.Load(t.Context(), metadata.ID)
	require.NoError(t, err)
	require.NotEmpty(t, loaded.Messages, "canceled turn history must persist")
	next := NewServer(sc)
	defer next.Close()
	require.NoError(t, next.StartBackground(t.Context()))
	time.Sleep(700 * time.Millisecond)
	require.EqualValues(t, 1, calls.Load(), "interrupted work must require explicit recovery")
	var answers int
	require.NoError(t, database.QueryRowContext(t.Context(), `SELECT count(*) FROM coordination_messages WHERE kind='answer'`).Scan(&answers))
	require.Zero(t, answers, "canceled turn must not produce success answer")
}

func TestCoordinationFailedVerificationNeverAnswersSuccess(t *testing.T) {
	database, _ := testpg.Open(t)
	sessions, err := session.NewPostgresStore(database)
	require.NoError(t, err)
	store, err := coordination.NewStore(t.Context(), database)
	require.NoError(t, err)
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		if calls.Add(1) == 1 {
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"write","type":"function","function":{"name":"fs","arguments":"{\"action\":\"write\",\"path\":\"broken.json\",\"content\":\"broken\"}"}}]},"finish_reason":"tool_calls"}]}`)
		} else {
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"Everything passed"},"finish_reason":"stop"}]}`)
		}
	}))
	defer provider.Close()
	front, _, err := store.RegisterPeer(t.Context(), coordination.Peer{ProjectID: "fixture", Name: "frontend", Role: "frontend", Workspace: t.TempDir()})
	require.NoError(t, err)
	back, _, err := store.RegisterPeer(t.Context(), coordination.Peer{ProjectID: "fixture", Name: "backend", Role: "backend", Workspace: t.TempDir()})
	require.NoError(t, err)
	metadata := session.NewSession(back.Workspace)
	metadata.Provider = "fixture"
	metadata.Model = "backend"
	require.NoError(t, sessions.Create(t.Context(), metadata))
	require.NoError(t, store.BindSession(t.Context(), back.ID, metadata.ID))
	baseFactory := coordinationRuntime(t)
	verifier := &requiredRuntimeVerifier{}
	server := NewServer(ServerConfig{Config: coordinationConfig(provider.URL), SessionStore: sessions, CoordStore: store, SessionRuntimeFactory: func(ctx context.Context, metadata *session.Session) (*SessionRuntime, error) {
		runtime, err := baseFactory(ctx, metadata)
		if runtime != nil {
			runtime.PostWriteVerifier = verifier
		}
		return runtime, err
	}})
	defer server.Close()
	message, err := store.Send(t.Context(), front.ID, coordination.SendInput{To: "backend", Kind: "request", Payload: json.RawMessage(`{"task":"write"}`), IdempotencyKey: "verify"})
	require.NoError(t, err)
	require.NoError(t, server.StartBackground(t.Context()))
	require.Eventually(t, func() bool {
		m, err := store.Read(t.Context(), back.ID, message.ID)
		return err == nil && m.State == coordination.Failed
	}, 5*time.Second, 20*time.Millisecond)
	loaded, err := sessions.Load(t.Context(), metadata.ID)
	require.NoError(t, err)
	encoded, _ := json.Marshal(loaded.Messages)
	require.Contains(t, string(encoded), "Post-write verification failed")
	require.NotContains(t, string(encoded), "Everything passed")
	var answers int
	require.NoError(t, database.QueryRowContext(t.Context(), `SELECT count(*) FROM coordination_messages WHERE kind='answer'`).Scan(&answers))
	require.Zero(t, answers)
	require.Eventually(t, func() bool { return server.CoordinationStatus()["delivery_error"] }, time.Second, 10*time.Millisecond)
}

func TestActualAgentCircularRequestStopsWithReadableEscalation(t *testing.T) {
	database, _ := testpg.Open(t)
	sessions, err := session.NewPostgresStore(database)
	require.NoError(t, err)
	store, err := coordination.NewStore(t.Context(), database)
	require.NoError(t, err)
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model    string            `json:"model"`
			Messages []json.RawMessage `json:"messages"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		w.Header().Set("Content-Type", "application/json")
		if body.Model == "backend" && calls.Add(1) == 1 {
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"cycle","type":"function","function":{"name":"agent_send","arguments":"{\"to\":\"frontend\",\"kind\":\"request\",\"payload\":\"ask the original sender again\",\"idempotency_key\":\"cycle\"}"}}]},"finish_reason":"tool_calls"}]}`)
		} else {
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"Dependency needs user review."},"finish_reason":"stop"}]}`)
		}
	}))
	defer provider.Close()
	var peers []coordination.Peer
	var metadata []*session.Session
	for _, name := range []string{"frontend", "backend"} {
		peer, _, err := store.RegisterPeer(t.Context(), coordination.Peer{ProjectID: "cycle-runtime", Name: name, Role: name, Workspace: t.TempDir()})
		require.NoError(t, err)
		sess := session.NewSession(peer.Workspace)
		sess.Provider = "fixture"
		sess.Model = name
		require.NoError(t, sessions.Create(t.Context(), sess))
		require.NoError(t, store.BindSession(t.Context(), peer.ID, sess.ID))
		peers = append(peers, peer)
		metadata = append(metadata, sess)
	}
	server := NewServer(ServerConfig{Config: coordinationConfig(provider.URL), SessionStore: sessions, CoordStore: store, SessionRuntimeFactory: func(ctx context.Context, metadata *session.Session) (*SessionRuntime, error) {
		peer, err := store.PeerForSession(ctx, metadata.ID)
		if err != nil {
			return nil, err
		}
		registry := tools.NewRegistry()
		for _, tool := range tools.NewCoordinationTools(store, peer.ID) {
			if err := registry.Register(tool); err != nil {
				return nil, err
			}
		}
		return &SessionRuntime{Tools: registry}, nil
	}})
	require.NoError(t, server.StartBackground(t.Context()))
	defer server.Close()
	request, err := store.Send(t.Context(), peers[0].ID, coordination.SendInput{To: "backend", Kind: "request", Payload: json.RawMessage(`"do backend work"`), IdempotencyKey: "start"})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		events, err := store.ReadEscalations(t.Context(), peers[1].ID, 0, 10)
		return err == nil && len(events) == 1
	}, 8*time.Second, 20*time.Millisecond)
	response, rpcErr := server.handleCoordination(t.Context(), "coordination.escalations", coordInputForRuntime(t, map[string]any{"session_id": metadata[0].ID, "limit": 1}))
	require.Nil(t, rpcErr)
	encoded, err := json.Marshal(response)
	require.NoError(t, err)
	var page struct {
		Escalations []coordination.Escalation `json:"escalations"`
		Cursor      int64                     `json:"next_cursor"`
		More        bool                      `json:"has_more"`
	}
	require.NoError(t, json.Unmarshal(encoded, &page))
	require.Len(t, page.Escalations, 1)
	require.Equal(t, request.ID, page.Escalations[0].ParentMessageID)
	require.Equal(t, coordination.Blocked, page.Escalations[0].State)
	require.True(t, page.Escalations[0].RequiresReview)
	require.Contains(t, page.Escalations[0].Reason, "cycle")
	require.Eventually(t, func() bool {
		message, err := store.Read(t.Context(), peers[0].ID, request.ID)
		return err == nil && message.State == coordination.Completed
	}, 5*time.Second, 20*time.Millisecond)
	require.LessOrEqual(t, calls.Load(), int32(3), "cycle must not trigger repeated model work")
	_, rpcErr = server.handleCoordination(t.Context(), "coordination.escalations", coordInputForRuntime(t, map[string]any{"session_id": metadata[0].ID, "after": -1}))
	require.NotNil(t, rpcErr)
	_, rpcErr = server.handleCoordination(t.Context(), "coordination.escalations", coordInputForRuntime(t, map[string]any{"session_id": metadata[0].ID, "from": peers[1].ID}))
	require.NotNil(t, rpcErr)
}

func coordInputForRuntime(t *testing.T, input any) []byte {
	t.Helper()
	data, err := json.Marshal(input)
	require.NoError(t, err)
	return data
}

package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
	"github.com/stretchr/testify/require"
)

func TestCanonicalSessionResolvesWorkspaceAndCopiesMetadata(t *testing.T) {
	workspace := t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	require.NoError(t, os.Symlink(workspace, alias))
	original := session.NewSession(alias)
	original.Metadata["fixture"] = "original"
	canonical, err := CanonicalSession(original)
	require.NoError(t, err)
	expected, err := filepath.EvalSymlinks(workspace)
	require.NoError(t, err)
	require.Equal(t, expected, canonical.Folder)
	canonical.Metadata["fixture"] = "changed"
	require.Equal(t, "original", original.Metadata["fixture"])
	original.ID = "../escape"
	_, err = CanonicalSession(original)
	require.Error(t, err)
}

func TestAPIUsesAndCleansPerSessionRuntime(t *testing.T) {
	store := &mockSessionStore{sessions: make(map[string]*session.Session)}
	cfg := config.Default()
	cfg.Agent.DefaultProvider = "fixture"
	cfg.Agent.DefaultModel = "fixture-model"
	cfg.Providers = []config.ProviderConfig{{Name: "fixture", Type: "openai", APIKey: "test-only"}}
	var built, closed atomic.Int32
	s := NewServer(ServerConfig{Config: cfg, SessionStore: store, SessionRuntimeFactory: func(_ context.Context, metadata *session.Session) (*SessionRuntime, error) {
		built.Add(1)
		registry := tools.NewRegistry()
		require.NoError(t, registry.Register(tools.NewFSTool(metadata.Folder)))
		return &SessionRuntime{Tools: registry, Cleanup: func() { closed.Add(1) }}, nil
	}})
	defer s.closeSessionRuntimes()
	for range 2 {
		metadata := session.NewSession(t.TempDir())
		require.NoError(t, store.Create(context.Background(), metadata))
		_, rpcErr := s.getOrCreateAgent(context.Background(), metadata.ID)
		require.Nil(t, rpcErr)
		_, rpcErr = s.getOrCreateAgent(context.Background(), metadata.ID)
		require.Nil(t, rpcErr)
		payload, err := json.Marshal(map[string]string{"id": metadata.ID})
		require.NoError(t, err)
		_, rpcErr = s.handleSessionDelete(context.Background(), payload)
		require.Nil(t, rpcErr)
	}
	require.EqualValues(t, 2, built.Load())
	require.EqualValues(t, 2, closed.Load())
}

type requiredRuntimeVerifier struct{ calls atomic.Int32 }

func (v *requiredRuntimeVerifier) IsWriteTool(name string) bool { return name == "fs" }
func (v *requiredRuntimeVerifier) ExtractWritePaths(_ string, _ json.RawMessage) []string {
	return []string{"written.json"}
}
func (v *requiredRuntimeVerifier) VerifyToolCall(context.Context, string, json.RawMessage) string {
	v.calls.Add(1)
	return "[verify] runtime verifier rejected this write"
}
func (v *requiredRuntimeVerifier) AuditTurnPaths([]string) string { return "" }

func TestAPISessionFactoryVerifierReachesExecutingAgent(t *testing.T) {
	var requests atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		w.Header().Set("Content-Type", "application/json")
		if requests.Add(1) == 1 {
			_, err := io.WriteString(w, `{"id":"fixture","choices":[{"index":0,"message":{"role":"assistant","tool_calls":[{"id":"real-write","type":"function","function":{"name":"fs","arguments":"{\"action\":\"write\",\"path\":\"written.json\",\"content\":\"{}\"}"}}]},"finish_reason":"tool_calls"}]}`)
			require.NoError(t, err)
		} else {
			encoded, err := json.Marshal(body)
			require.NoError(t, err)
			require.Contains(t, string(encoded), "runtime verifier rejected")
			_, err = io.WriteString(w, `{"id":"fixture","choices":[{"index":0,"message":{"role":"assistant","content":"All checks passed"},"finish_reason":"stop"}]}`)
			require.NoError(t, err)
		}
	}))
	defer provider.Close()
	cfg := config.Default()
	cfg.Agent.DefaultProvider = "fixture"
	cfg.Agent.DefaultModel = "gpt-4o-mini"
	cfg.Providers = []config.ProviderConfig{{Name: "fixture", Type: "openai", APIKey: "test-only", BaseURL: provider.URL}}
	metadata := session.NewSession(t.TempDir())
	store := &mockSessionStore{sessions: map[string]*session.Session{metadata.ID: metadata}}
	verifier := &requiredRuntimeVerifier{}
	server := NewServer(ServerConfig{Config: cfg, SessionStore: store, SessionRuntimeFactory: func(_ context.Context, metadata *session.Session) (*SessionRuntime, error) {
		registry := tools.NewRegistry()
		require.NoError(t, registry.Register(tools.NewFSTool(metadata.Folder)))
		return &SessionRuntime{Tools: registry, PostWriteVerifier: verifier}, nil
	}})
	defer server.Close()
	input, err := json.Marshal(map[string]string{"session_id": metadata.ID, "message": "Write a JSON file"})
	require.NoError(t, err)
	result, rpcErr := server.handleAgentSend(context.Background(), input)
	require.Nil(t, rpcErr)
	response := result.(*SendMessageResult)
	require.True(t, response.Blocked)
	require.NotContains(t, response.Response, "All checks passed")
	require.GreaterOrEqual(t, verifier.calls.Load(), int32(1))
	data, err := os.ReadFile(filepath.Join(metadata.Folder, "written.json"))
	require.NoError(t, err)
	require.JSONEq(t, "{}", string(data))
}

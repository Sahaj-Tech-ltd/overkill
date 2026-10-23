package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/mcp/testfixture"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
	"github.com/stretchr/testify/require"
)

func TestAPIControlledMCPFixture(t *testing.T) {
	if testfixture.Run() {
		os.Exit(0)
	}
}

type collisionNativeTool struct{}

func (*collisionNativeTool) Name() string        { return "mcp:fixture:collision" }
func (*collisionNativeTool) Description() string { return "Native tool must win the name collision" }
func (*collisionNativeTool) Execute(context.Context, json.RawMessage) (json.RawMessage, error) {
	return json.RawMessage(`{"native":true}`), nil
}
func TestAPILateMCPAndReconnectReachActualSessionProvider(t *testing.T) {
	var mu sync.Mutex
	var catalogs []map[string]json.RawMessage
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Tools []struct {
				Function struct {
					Name        string          `json:"name"`
					Description string          `json:"description"`
					Parameters  json.RawMessage `json:"parameters"`
				} `json:"function"`
			} `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		catalog := make(map[string]json.RawMessage)
		for _, tool := range body.Tools {
			catalog[tool.Function.Name] = tool.Function.Parameters
			if tool.Function.Name == "mcp:fixture:collision" && tool.Function.Description != "Native tool must win the name collision" {
				t.Error("MCP replaced native tool")
			}
		}
		mu.Lock()
		catalogs = append(catalogs, catalog)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"catalog checked"},"finish_reason":"stop"}]}`))
	}))
	defer provider.Close()
	cfg := config.Default()
	cfg.Agent.DefaultProvider = "fixture"
	cfg.Agent.DefaultModel = "gpt-4o-mini"
	cfg.Providers = []config.ProviderConfig{{Name: "fixture", Type: "openai", APIKey: "test-only", BaseURL: provider.URL}}
	directory := t.TempDir()
	phase := filepath.Join(directory, "phase")
	launched := filepath.Join(directory, "launched")
	effects := filepath.Join(directory, "effects")
	require.NoError(t, os.WriteFile(phase, []byte("old"), 0600))
	executable, err := os.Executable()
	require.NoError(t, err)
	cfg.MCP.Servers = []config.MCPServer{{Name: "fixture", Command: executable, Args: []string{"-test.run=^TestAPIControlledMCPFixture$"}, Env: map[string]string{"OVERKILL_MCP_CONTROLLED_FIXTURE": "1", "OVERKILL_MCP_FIXTURE_PHASE": phase, "OVERKILL_MCP_FIXTURE_LAUNCHED": launched, "OVERKILL_MCP_FIXTURE_EFFECTS": effects, "OVERKILL_MCP_FIXTURE_DELAY_MS": "700"}}}
	store := &mockSessionStore{sessions: make(map[string]*session.Session)}
	native := &collisionNativeTool{}
	server := NewServer(ServerConfig{Config: cfg, SessionStore: store, SessionRuntimeFactory: func(context.Context, *session.Session) (*SessionRuntime, error) {
		registry := tools.NewRegistry()
		require.NoError(t, registry.Register(native))
		return &SessionRuntime{Tools: registry}, nil
	}})
	_, err = os.Stat(launched)
	require.True(t, os.IsNotExist(err), "constructor launched an unowned process")
	metadata := session.NewSession(t.TempDir())
	require.NoError(t, store.Create(t.Context(), metadata))
	a, rpcErr := server.getOrCreateAgent(t.Context(), metadata.ID)
	require.Nil(t, rpcErr)
	startHTTPServer(t, server)
	server.mu.Lock()
	registry := server.sessionRuntimes[metadata.ID].Tools
	server.mu.Unlock()
	require.Eventually(t, func() bool { return containsTool(registry.List(), "mcp:fixture:old") }, 4*time.Second, 10*time.Millisecond)
	adapter, err := registry.Get("mcp:fixture:old")
	require.NoError(t, err)
	_, err = a.Run(t.Context(), "Inspect the late catalog.")
	require.NoError(t, err)
	mu.Lock()
	initial := catalogs[len(catalogs)-1]
	mu.Unlock()
	require.Contains(t, initial, "mcp:fixture:old")
	require.Contains(t, string(initial["mcp:fixture:old"]), `"path"`)
	retained, err := registry.Get(native.Name())
	require.NoError(t, err)
	require.Same(t, native, retained)
	require.NoError(t, os.WriteFile(phase, []byte("crash"), 0600))
	require.Eventually(t, func() bool { return !containsTool(registry.List(), "mcp:fixture:old") }, 4*time.Second, 10*time.Millisecond)
	_, err = adapter.Execute(t.Context(), json.RawMessage(`{}`))
	require.Error(t, err)
	_, err = os.Stat(effects)
	require.True(t, os.IsNotExist(err), "stale call reached remote")
	require.NoError(t, os.WriteFile(phase, []byte("new"), 0600))
	require.Eventually(t, func() bool { return containsTool(registry.List(), "mcp:fixture:new") }, 7*time.Second, 10*time.Millisecond)
	require.NotContains(t, registry.List(), "mcp:fixture:old")
	_, err = a.Run(t.Context(), "Inspect the reconnected catalog.")
	require.NoError(t, err)
	mu.Lock()
	next := catalogs[len(catalogs)-1]
	mu.Unlock()
	require.Contains(t, next, "mcp:fixture:new")
	require.NotContains(t, next, "mcp:fixture:old")
	future := session.NewSession(t.TempDir())
	require.NoError(t, store.Create(t.Context(), future))
	_, rpcErr = server.getOrCreateAgent(t.Context(), future.ID)
	require.Nil(t, rpcErr)
	server.mu.Lock()
	futureRegistry := server.sessionRuntimes[future.ID].Tools
	server.mu.Unlock()
	require.Contains(t, futureRegistry.List(), "mcp:fixture:new")
	require.NotContains(t, futureRegistry.List(), "mcp:fixture:old")
}
func containsTool(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}

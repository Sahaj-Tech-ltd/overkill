//go:build unix

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/extensions"
	"github.com/Sahaj-Tech-ltd/overkill/internal/hooks"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/skills"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
	"github.com/stretchr/testify/require"
)

func TestUnifiedExtensionsActualHTTPInventoriesAndRuntimeToggles(t *testing.T) {
	home := t.TempDir()
	marker := filepath.Join(home, "hook-runs")
	hookDir := filepath.Join(home, "hooks", "before_tool_call")
	require.NoError(t, os.MkdirAll(hookDir, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(hookDir, "capture.sh"), []byte(fmt.Sprintf("#!/bin/sh\nprintf 'hit\\n' >> %q\n", marker)), 0700))
	plugins := filepath.Join(home, "plugins")
	require.NoError(t, os.MkdirAll(plugins, 0700))
	pluginMarker := filepath.Join(home, "plugin-must-not-run")
	require.NoError(t, os.WriteFile(filepath.Join(plugins, "installed-plugin"), []byte(fmt.Sprintf("#!/bin/sh\ntouch %q\n", pluginMarker)), 0700))
	reg, err := skills.NewPersistentRegistry(filepath.Join(home, "skill-state"))
	require.NoError(t, err)
	require.NoError(t, reg.Register(&skills.Skill{Name: "extension-skill", Description: "Extension acceptance skill", Instructions: "EXTENSION_SKILL_SENTINEL check workspace criteria.", Enabled: true}))
	prompts := make(chan string, 8)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct{ Role, Content string } `json:"messages"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		prompt := ""
		for _, message := range body.Messages {
			if message.Role == "system" {
				prompt += message.Content
			}
		}
		prompts <- prompt
		w.Header().Set("Content-Type", "application/json")
		if len(body.Messages) > 0 && body.Messages[len(body.Messages)-1].Role == "tool" {
			fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"extension fixture completed"},"finish_reason":"stop"}]}`)
		} else {
			fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"extension-read","type":"function","function":{"name":"fs","arguments":"{\"action\":\"list\",\"path\":\".\"}"}}]},"finish_reason":"tool_calls"}]}`)
		}
	}))
	defer provider.Close()
	cfg := config.Default()
	cfg.Agent.DefaultProvider, cfg.Agent.DefaultModel = "fixture", "fixture-model"
	cfg.Rewriter.Enabled = false
	cfg.Plugins.Dir = plugins
	cfg.Providers = []config.ProviderConfig{{Name: "fixture", Type: "openai", APIKey: "fixture-only", BaseURL: provider.URL, Models: []config.ModelConfig{{ID: "fixture-model", ContextWindow: 32000, MaxOutputTokens: 1024}}}}
	cfg.MCP.Servers = []config.MCPServer{{Name: "extension-mcp", Command: os.Args[0], Args: []string{"-test.run=^TestAPIControlledMCPFixture$"}, Env: map[string]string{"OVERKILL_MCP_CONTROLLED_FIXTURE": "1", "PRIVATE_EXTENSION_SECRET": "must-not-appear"}}}
	store := &mockSessionStore{sessions: map[string]*session.Session{}}
	server := NewServer(ServerConfig{Config: cfg, SessionStore: store, SkillsRegistry: reg, ExtensionsManager: extensions.NewConfiguredManager(reg, cfg, home), SessionRuntimeFactory: func(ctx context.Context, metadata *session.Session) (*SessionRuntime, error) {
		registry, err := hooks.NewRegistryFromDir(filepath.Join(home, "hooks"))
		return &SessionRuntime{Hooks: registry, Tools: tools.NewDefaultRegistry(tools.FactoryDeps{CWD: metadata.Folder})}, err
	}})
	startHTTPServer(t, server)
	sess := session.NewSession(t.TempDir())
	require.NoError(t, store.Create(t.Context(), sess))
	a, rpcErr := server.getOrCreateAgent(t.Context(), sess.ID)
	require.Nil(t, rpcErr)
	list := func() map[string]extensions.Extension {
		result := sidebarRPC(t, server, "extensions.list", map[string]string{"session_id": sess.ID})
		require.Nil(t, result.Error)
		data, err := json.Marshal(result.Result)
		require.NoError(t, err)
		require.NotContains(t, string(data), "must-not-appear")
		var body struct {
			Extensions []extensions.Extension `json:"extensions"`
		}
		require.NoError(t, json.Unmarshal(data, &body))
		out := map[string]extensions.Extension{}
		for _, item := range body.Extensions {
			out[string(item.Kind)+":"+item.ID] = item
		}
		return out
	}
	require.Eventually(t, func() bool { return list()["mcp:extension-mcp"].Enabled }, 10*time.Second, 50*time.Millisecond)
	inventory := list()
	require.True(t, inventory["hook:file:before_tool_call/capture"].CanToggle)
	require.False(t, inventory["plugin:installed-plugin"].Enabled)
	require.Equal(t, "discovered_not_running", inventory["plugin:installed-plugin"].Metadata["runtime_state"])
	toggle := func(kind, id string, enabled bool) {
		result := sidebarRPC(t, server, "extensions.toggle", map[string]any{"session_id": sess.ID, "kind": kind, "id": id, "enabled": enabled})
		require.Nil(t, result.Error)
	}
	run := func() string {
		_, err := a.Run(t.Context(), "Check these workspace criteria.")
		require.NoError(t, err)
		first := <-prompts
		<-prompts
		return first
	}
	require.Contains(t, run(), "EXTENSION_SKILL_SENTINEL")
	before, err := os.ReadFile(marker)
	require.NoError(t, err)
	require.NotEmpty(t, before)
	toggle("hook", "file:before_tool_call/capture", false)
	toggle("skill", "extension-skill", false)
	require.NotContains(t, run(), "EXTENSION_SKILL_SENTINEL")
	after, err := os.ReadFile(marker)
	require.NoError(t, err)
	require.Equal(t, string(before), string(after))
	toggle("hook", "file:before_tool_call/capture", true)
	toggle("skill", "extension-skill", true)
	require.Contains(t, run(), "EXTENSION_SKILL_SENTINEL")
	after, err = os.ReadFile(marker)
	require.NoError(t, err)
	require.Greater(t, strings.Count(string(after), "hit"), strings.Count(string(before), "hit"))
	for _, item := range []struct{ kind, id string }{{"plugin", "installed-plugin"}, {"mcp", "extension-mcp"}} {
		response := sidebarRPC(t, server, "extensions.toggle", map[string]any{"session_id": sess.ID, "kind": item.kind, "id": item.id, "enabled": false})
		require.NotNil(t, response.Error)
		require.Contains(t, response.Error.Message, "unsupported")
	}
	_, err = os.Stat(pluginMarker)
	require.ErrorIs(t, err, os.ErrNotExist)
}

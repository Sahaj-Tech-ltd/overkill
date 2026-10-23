package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/skills"
	"github.com/stretchr/testify/require"
)

// Exercise the production session factory rather than installing the registry
// directly on a test-created Agent. The wire request is the acceptance boundary.
func TestSkillRegistryReachesProductionSessionProvider(t *testing.T) {
	prompts := make(chan string, 4)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Messages []struct{ Role, Content string } `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
			http.Error(w, "invalid fixture request", http.StatusBadRequest)
			return
		}
		var prompt string
		for _, message := range input.Messages {
			if message.Role == "system" {
				prompt += message.Content
			}
		}
		prompts <- prompt
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"skill fixture completed"},"finish_reason":"stop"}]}`))
	}))
	defer provider.Close()
	cfg := config.Default()
	cfg.Agent.DefaultProvider, cfg.Agent.DefaultModel = "fixture", "gpt-4o-mini"
	cfg.Providers = []config.ProviderConfig{{Name: "fixture", Type: "openai", APIKey: "fixture-only", BaseURL: provider.URL}}
	directory := filepath.Join(t.TempDir(), "skill-state")
	registry, err := skills.NewPersistentRegistry(directory)
	require.NoError(t, err)
	const sentinel = "SKILL_FACTORY_SENTINEL: Check the workspace acceptance criteria."
	skill := &skills.Skill{Name: "factory-fixture", Description: "Runtime acceptance fixture", Instructions: sentinel, Enabled: true}
	require.NoError(t, registry.Register(skill))
	store := &mockSessionStore{sessions: make(map[string]*session.Session)}
	server := NewServer(ServerConfig{Config: cfg, SkillsRegistry: registry, SessionStore: store})
	startHTTPServer(t, server)
	metadata := session.NewSession(t.TempDir())
	require.NoError(t, store.Create(t.Context(), metadata))
	a, rpcErr := server.getOrCreateAgent(t.Context(), metadata.ID)
	require.Nil(t, rpcErr)
	_, err = a.Run(t.Context(), "Check this workspace.")
	require.NoError(t, err)
	require.Contains(t, <-prompts, sentinel)
	response := sidebarRPC(t, server, "skills.toggle", map[string]any{"name": skill.Name, "enabled": false})
	require.Nil(t, response.Error)
	_, err = a.Run(t.Context(), "Check this workspace again.")
	require.NoError(t, err)
	require.NotContains(t, <-prompts, sentinel)
	// A later session must receive the same authoritative disabled registry.
	other := session.NewSession(t.TempDir())
	require.NoError(t, store.Create(t.Context(), other))
	b, rpcErr := server.getOrCreateAgent(t.Context(), other.ID)
	require.Nil(t, rpcErr)
	_, err = b.Run(t.Context(), "Check the later workspace.")
	require.NoError(t, err)
	require.NotContains(t, <-prompts, sentinel)
	reopened, err := skills.NewPersistentRegistry(directory)
	require.NoError(t, err)
	require.NoError(t, reopened.Register(skill))
	persisted, ok := reopened.Get(skill.Name)
	require.True(t, ok)
	require.False(t, persisted.Enabled)
}

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/skills"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tasks"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
	"github.com/stretchr/testify/require"
)

func sidebarRPC(t *testing.T, s *Server, method string, params any) Response {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
	require.NoError(t, err)
	req, err := http.NewRequest("POST", s.Addr()+"/rpc", strings.NewReader(string(payload)))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+s.AuthToken())
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, 200, res.StatusCode)
	var response Response
	require.NoError(t, json.NewDecoder(res.Body).Decode(&response))
	return response
}

func TestTodoSessionIsolationAndEditingThroughHTTP(t *testing.T) {
	store := tasks.NewStore(t.TempDir())
	s := NewServer(ServerConfig{Config: config.Default(), TasksStore: &TasksStoreAdapter{S: store}})
	startHTTPServer(t, s)
	added := sidebarRPC(t, s, "todo.add", map[string]string{"session_id": "session-a", "description": "Fix backend auth"})
	require.Nil(t, added.Error)
	data, err := json.Marshal(added.Result)
	require.NoError(t, err)
	var task TaskDTO
	require.NoError(t, json.Unmarshal(data, &task))
	other := sidebarRPC(t, s, "todo.list", map[string]string{"session_id": "session-b"})
	require.Nil(t, other.Error)
	require.Empty(t, other.Result)
	wrong := sidebarRPC(t, s, "todo.update", map[string]string{"id": task.ID, "session_id": "session-b", "description": "Wrong session"})
	require.NotNil(t, wrong.Error)
	updated := sidebarRPC(t, s, "todo.update", map[string]string{"id": task.ID, "session_id": "session-a", "description": "Fix authenticated streaming"})
	require.Nil(t, updated.Error)
	saved, err := store.Get(task.ID)
	require.NoError(t, err)
	require.Equal(t, "Fix authenticated streaming", saved.Intent)
	blank := sidebarRPC(t, s, "todo.add", map[string]string{"session_id": "session-a", "description": "  "})
	require.NotNil(t, blank.Error)
}

type promptCaptureProvider struct {
	streamingTestProvider
	prompt string
}

func (p *promptCaptureProvider) Complete(_ context.Context, request providers.Request) (providers.Response, error) {
	p.prompt = request.SystemPrompt
	return providers.Response{Content: "fixture reply"}, nil
}

func TestSkillToggleControlsExecutingPromptAndPersistsThroughHTTP(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "state")
	reg, err := skills.NewPersistentRegistry(dir)
	require.NoError(t, err)
	skill := &skills.Skill{Name: "prompt-skill", Description: "A skill with a visible prompt instruction", Instructions: "SKILL_SENTINEL: Follow this exact fixture instruction.", Enabled: true}
	require.NoError(t, reg.Register(skill))
	s := NewServer(ServerConfig{Config: config.Default(), SkillsRegistry: reg})
	startHTTPServer(t, s)
	provider := &promptCaptureProvider{}
	a := agent.New(agent.Config{Provider: provider, Tools: tools.NewRegistry(), Model: "fixture"})
	defer a.Shutdown()
	a.SetSkillRegistry(reg)
	_, err = a.Run(context.Background(), "hello")
	require.NoError(t, err)
	require.Contains(t, provider.prompt, "SKILL_SENTINEL")
	for i := 0; i < 2; i++ {
		response := sidebarRPC(t, s, "skills.toggle", map[string]any{"name": "prompt-skill", "enabled": false})
		require.Nil(t, response.Error)
	}
	_, err = a.Run(context.Background(), "hello again")
	require.NoError(t, err)
	require.NotContains(t, provider.prompt, "SKILL_SENTINEL")
	reopened, err := skills.NewPersistentRegistry(dir)
	require.NoError(t, err)
	require.NoError(t, reopened.Register(skill))
	stored, _ := reopened.Get("prompt-skill")
	require.False(t, stored.Enabled)
	require.NotNil(t, sidebarRPC(t, s, "skills.toggle", map[string]any{"name": "unknown", "enabled": false}).Error)
}

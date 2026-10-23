package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/instructions"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/stretchr/testify/require"
)

func TestAPIScopedInstructionsReachRunStreamAndResumedSession(t *testing.T) {
	prompts := make(chan string, 8)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Stream   bool
			Messages []struct{ Role, Content string }
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
			http.Error(w, "bad request", 400)
			return
		}
		var prompt string
		for _, message := range input.Messages {
			if message.Role == "system" {
				prompt += message.Content
			}
		}
		prompts <- prompt
		if input.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}]}`)
	}))
	defer provider.Close()
	cfg := rewriterConfig(0, provider.URL)
	cfg.Agent.Instructions = instructions.Config{Enabled: true, Directory: "nested"}
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "nested"), 0700))
	path := filepath.Join(root, "nested", "AGENTS.md")
	require.NoError(t, os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("api-root-marker"), 0600))
	require.NoError(t, os.WriteFile(path, []byte("api-directory-marker"), 0600))
	store := &mockSessionStore{sessions: make(map[string]*session.Session)}
	metadata := session.NewSession(root)
	require.NoError(t, store.Create(t.Context(), metadata))
	server := NewServer(ServerConfig{Config: cfg, SessionStore: store})
	a, err := server.createAgent(t.Context(), metadata.ID)
	require.NoError(t, err)
	defer a.Shutdown()
	_, err = a.Run(t.Context(), "inspect")
	require.NoError(t, err)
	require.Contains(t, <-prompts, "api-directory-marker")
	require.NoError(t, os.WriteFile(path, []byte("api-edited-marker"), 0600))
	events, err := a.Stream(t.Context(), "inspect again")
	require.NoError(t, err)
	for event := range events {
		require.NoError(t, event.Error)
		if event.Type == agent.EventDone {
			require.NotNil(t, event.Result)
		}
	}
	prompt := <-prompts
	require.Contains(t, prompt, "api-edited-marker")
	require.NotContains(t, prompt, "api-directory-marker")
	require.NoError(t, os.Remove(path))
	metadata.Messages = a.History()
	resumed, err := server.createAgent(t.Context(), metadata.ID)
	require.NoError(t, err)
	defer resumed.Shutdown()
	_, err = resumed.Run(t.Context(), "resume")
	require.NoError(t, err)
	prompt = <-prompts
	require.Contains(t, prompt, "api-root-marker")
	require.NotContains(t, prompt, "api-edited-marker")
	resumed.SetInstructionSource(nil)
	_, err = resumed.Run(t.Context(), "revoked")
	require.NoError(t, err)
	require.NotContains(t, <-prompts, "api-root-marker")
}

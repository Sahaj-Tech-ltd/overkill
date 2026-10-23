package api

import (
	"context"
	"encoding/json"
	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/automemory"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// No test setter: production API provider construction and post-turn lifecycle
// must make the extraction request, save it and reload it in a later session.
func TestAPIAutomaticMemoryProviderPersistenceReloadAndDisable(t *testing.T) {
	for _, mode := range []string{"enabled", "default-disabled", "user-disabled"} {
		t.Run(mode, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("OVERKILL_HOME", home)
			workspace := t.TempDir()
			if mode == "user-disabled" {
				require.NoError(t, os.WriteFile(filepath.Join(home, "user.yaml"), []byte("advanced:\n  memory:\n    enabled: false\n"), 0600))
			}
			var mu sync.Mutex
			var prompts []string
			extractions := 0
			fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Stream    bool                             `json:"stream"`
					Messages  []struct{ Role, Content string } `json:"messages"`
					Tools     []any                            `json:"tools"`
					MaxTokens int                              `json:"max_tokens"`
				}
				if json.NewDecoder(r.Body).Decode(&request) != nil {
					http.Error(w, "invalid request", 400)
					return
				}
				var prompt, user string
				for _, m := range request.Messages {
					if m.Role == "system" {
						prompt += m.Content
					}
					if m.Role == "user" {
						user = m.Content
					}
				}
				content := "Assistant generated claim must never become extraction input."
				mu.Lock()
				if strings.HasPrefix(prompt, "Extract durable, non-sensitive facts") {
					extractions++
					require.Empty(t, request.Tools)
					require.NotContains(t, user, "Assistant generated claim")
					require.NotContains(t, user, "TOOL_OUTPUT_UNTRUSTED")
					content = `{"facts":[{"category":"user","content":"User prefers compact diffs."},{"category":"user","content":"User prefers compact diffs."}]}`
				} else {
					prompts = append(prompts, prompt)
				}
				mu.Unlock()
				if request.Stream {
					w.Header().Set("Content-Type", "text/event-stream")
					chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": content}, "finish_reason": nil}}})
					_, _ = w.Write(append(append([]byte("data: "), chunk...), []byte("\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")...))
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": content}, "finish_reason": "stop"}}})
			}))
			defer fixture.Close()
			cfg := rewriterConfig(0, fixture.URL)
			cfg.AutoMemory = config.AutoMemoryConfig{Enabled: mode != "default-disabled", MinNewMessages: 1}
			store := &mockSessionStore{sessions: make(map[string]*session.Session)}
			server := &Server{cfg: cfg, sessionStore: store, toolRegistry: tools.NewRegistry()}
			makeAgent := func() *agent.Agent {
				metadata := session.NewSession(workspace)
				require.NoError(t, store.Create(t.Context(), metadata))
				a, err := server.createAgent(t.Context(), metadata.ID)
				require.NoError(t, err)
				t.Cleanup(a.Shutdown)
				return a
			}
			first := makeAgent()
			require.NoError(t, first.Inject(providers.Message{Role: "tool", Content: "TOOL_OUTPUT_UNTRUSTED"}))
			_, err := first.Run(t.Context(), "I prefer compact diffs.")
			require.NoError(t, err)
			first.Shutdown()
			second := makeAgent()
			events, err := second.Stream(t.Context(), "What format should you use?")
			require.NoError(t, err)
			completed := false
			for event := range events {
				require.NoError(t, event.Error)
				if event.Type == agent.EventDone {
					completed = true
				}
			}
			require.True(t, completed)
			mu.Lock()
			require.Len(t, prompts, 2)
			if mode == "enabled" {
				require.Equal(t, 2, extractions)
				require.Contains(t, prompts[1], "User prefers compact diffs.")
				require.Contains(t, prompts[1], "untrusted reference DATA")
			} else {
				require.Zero(t, extractions)
				require.NotContains(t, prompts[1], "Saved memory facts")
			}
			mu.Unlock()
			files, err := filepath.Glob(filepath.Join(home, "memory", "*", "facts.json"))
			require.NoError(t, err)
			if mode == "enabled" {
				require.Len(t, files, 1)
				data, err := os.ReadFile(files[0])
				require.NoError(t, err)
				var facts []automemory.Fact
				require.NoError(t, json.Unmarshal(data, &facts))
				require.Len(t, facts, 1)
				info, err := os.Stat(files[0])
				require.NoError(t, err)
				require.Equal(t, os.FileMode(0600), info.Mode().Perm())
				// Runtime disable also affects already constructed extractors and reload.
				require.NoError(t, os.WriteFile(filepath.Join(home, "user.yaml"), []byte("advanced:\n  memory:\n    enabled: false\n"), 0600))
				_, err = second.Run(context.Background(), "Do not save this turn.")
				require.NoError(t, err)
				mu.Lock()
				require.Equal(t, 2, extractions)
				require.NotContains(t, prompts[2], "Saved memory facts")
				mu.Unlock()
			} else {
				require.Empty(t, files)
			}
		})
	}
}

func TestAPIAutomaticMemoryShutdownCancelsExtractionHTTPWithoutPublishing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OVERKILL_HOME", home)
	started, canceled := make(chan struct{}), make(chan struct{})
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []struct{ Role, Content string } `json:"messages"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil {
			http.Error(w, "invalid request", 400)
			return
		}
		for _, message := range request.Messages {
			if message.Role == "system" && strings.HasPrefix(message.Content, "Extract durable, non-sensitive facts") {
				close(started)
				select {
				case <-r.Context().Done():
					close(canceled)
				case <-time.After(3 * time.Second):
					t.Error("extraction HTTP request outlived session shutdown")
				}
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"Completed answer."},"finish_reason":"stop"}]}`))
	}))
	defer fixture.Close()
	cfg := rewriterConfig(0, fixture.URL)
	cfg.AutoMemory = config.AutoMemoryConfig{Enabled: true, MinNewMessages: 1}
	store := &mockSessionStore{sessions: make(map[string]*session.Session)}
	metadata := session.NewSession(t.TempDir())
	require.NoError(t, store.Create(t.Context(), metadata))
	server := &Server{cfg: cfg, sessionStore: store, toolRegistry: tools.NewRegistry()}
	a, err := server.createAgent(t.Context(), metadata.ID)
	require.NoError(t, err)
	defer a.Shutdown()
	done := make(chan error, 1)
	go func() { _, err := a.Run(t.Context(), "I prefer compact diffs."); done <- err }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("no production extraction request")
	}
	a.Shutdown()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("post-turn extraction ignored shutdown")
	}
	select {
	case <-canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("HTTP extraction did not cancel")
	}
	files, err := filepath.Glob(filepath.Join(home, "memory", "*", "facts.json"))
	require.NoError(t, err)
	require.Empty(t, files)
}

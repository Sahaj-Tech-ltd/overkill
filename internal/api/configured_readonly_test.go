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
	"sync/atomic"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
	"github.com/stretchr/testify/require"
)

func TestConfiguredReadonlyReachesActualAPIRunAndStream(t *testing.T) {
	for _, level := range []string{"readonly", "supervised"} {
		t.Run(level, func(t *testing.T) {
			var calls atomic.Int32
			toolFeedback := make(chan string, 2)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Stream   bool `json:"stream"`
					Messages []struct {
						Role    string          `json:"role"`
						Content json.RawMessage `json:"content"`
					} `json:"messages"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					http.Error(w, "invalid request", 400)
					return
				}
				var message map[string]any
				finish := "stop"
				if calls.Add(1)%2 == 1 {
					finish = "tool_calls"
					message = map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "attempt-write", "type": "function", "function": map[string]any{"name": "fs", "arguments": `{"action":"write","path":"proof.txt","content":"changed"}`}}}}
				} else {
					feedback := ""
					for _, m := range body.Messages {
						if m.Role == "tool" {
							feedback += string(m.Content)
						}
					}
					toolFeedback <- feedback
					message = map[string]any{"content": "Tool result observed."}
				}
				if body.Stream {
					w.Header().Set("Content-Type", "text/event-stream")
					chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": message, "finish_reason": finish}}})
					fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
				} else {
					message["role"] = "assistant"
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": message, "finish_reason": finish}}})
				}
			}))
			defer upstream.Close()
			cfg := config.Default()
			cfg.Security.AutonomyLevel = level
			cfg.Agent.DefaultProvider = "fixture"
			cfg.Agent.DefaultModel = "gpt-4o-mini"
			cfg.Providers = []config.ProviderConfig{{Name: "fixture", Type: "openai", APIKey: "fixture-only", BaseURL: upstream.URL}}
			metadata := session.NewSession(t.TempDir())
			store := &mockSessionStore{sessions: map[string]*session.Session{metadata.ID: metadata}}
			s := NewServer(ServerConfig{Config: cfg, SessionStore: store, SessionRuntimeFactory: func(_ context.Context, sess *session.Session) (*SessionRuntime, error) {
				registry := tools.NewRegistry()
				require.NoError(t, registry.Register(tools.NewFSTool(sess.Folder)))
				return &SessionRuntime{Tools: registry}, nil
			}})
			defer s.Close()
			a, rpcErr := s.getOrCreateAgent(t.Context(), metadata.ID)
			require.Nil(t, rpcErr)
			for _, stream := range []bool{false, true} {
				if stream {
					events, err := a.Stream(t.Context(), "Try writing proof.txt")
					require.NoError(t, err)
					done := false
					for e := range events {
						require.NoError(t, e.Error)
						if e.Type == agent.EventDone {
							done = true
						}
					}
					require.True(t, done)
				} else {
					_, err := a.Run(t.Context(), "Try writing proof.txt")
					require.NoError(t, err)
				}
				feedback := <-toolFeedback
				data, err := os.ReadFile(filepath.Join(metadata.Folder, "proof.txt"))
				if level == "readonly" {
					require.True(t, os.IsNotExist(err))
					require.Contains(t, feedback, "configured security.autonomy_level=readonly")
				} else {
					require.NoError(t, err)
					require.Equal(t, "changed", string(data))
					require.False(t, strings.Contains(feedback, "write denied"))
					require.NoError(t, os.Remove(filepath.Join(metadata.Folder, "proof.txt")))
				}
			}
			require.EqualValues(t, 4, calls.Load())
		})
	}
}

package api

import (
	"encoding/json"
	"fmt"
	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAPIPersonalityLevelsReachActualRunAndStreamProvider(t *testing.T) {
	for _, level := range []string{"none", "off", "subtle", "witty", "full"} {
		t.Run(level, func(t *testing.T) {
			prompts := make(chan string, 2)
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					Stream   bool                             `json:"stream"`
					Messages []struct{ Role, Content string } `json:"messages"`
				}
				require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
				prompt := ""
				for _, m := range req.Messages {
					if m.Role == "system" {
						prompt += m.Content
					}
				}
				prompts <- prompt
				content := "neutral fixture output"
				for _, candidate := range []string{"subtle", "witty", "full"} {
					if strings.Contains(prompt, "Communication style ("+candidate+")") {
						content = candidate + " fixture output"
					}
				}
				if req.Stream {
					w.Header().Set("Content-Type", "text/event-stream")
					chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": content}, "finish_reason": "stop"}}})
					fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": content}, "finish_reason": "stop"}}})
			}))
			defer provider.Close()
			cfg := rewriterConfig(0, provider.URL)
			cfg.Personality.Level = level
			cfg.Personality.Language = "ja"
			cfg.Agent.SystemPrompt = "HOST_RULES: Return concise results. Do not claim unverified work."
			store := &mockSessionStore{sessions: make(map[string]*session.Session)}
			metadata := session.NewSession(t.TempDir())
			require.NoError(t, store.Create(t.Context(), metadata))
			server := &Server{cfg: cfg, sessionStore: store, toolRegistry: tools.NewRegistry()}
			a, err := server.createAgent(t.Context(), metadata.ID)
			require.NoError(t, err)
			defer a.Shutdown()
			result, err := a.Run(t.Context(), "Explain the configuration.")
			require.NoError(t, err)
			expected := level + " fixture output"
			if level == "off" || level == "none" {
				expected = "neutral fixture output"
			}
			require.Equal(t, expected, result.Response)
			events, err := a.Stream(t.Context(), "Explain once more.")
			require.NoError(t, err)
			done := false
			for event := range events {
				require.NoError(t, event.Error)
				if event.Type == agent.EventDone {
					done = true
					require.Equal(t, expected, event.Result.Response)
				}
			}
			require.True(t, done)
			for range 2 {
				prompt := <-prompts
				require.Contains(t, prompt, cfg.Agent.SystemPrompt)
				require.Contains(t, prompt, `locale "ja"`)
				if level == "off" || level == "none" {
					require.NotContains(t, prompt, "Communication style")
				} else {
					require.Contains(t, prompt, "Communication style ("+level+")")
					require.Contains(t, prompt, "security boundaries")
					require.Contains(t, prompt, "requested output format take priority")
				}
			}
		})
	}
}

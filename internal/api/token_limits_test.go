package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
	"github.com/stretchr/testify/require"
)

func TestAPITokenLimitsHTTPPayload(t *testing.T) {
	for _, tc := range []struct {
		name           string
		model          config.ModelConfig
		override, want int
		invalid        bool
	}{
		{"legacy context omitted from output", config.ModelConfig{ID: "fixture", MaxTokens: 200000}, 0, 0, false},
		{"separate model output", config.ModelConfig{ID: "fixture", ContextWindow: 128000, MaxOutputTokens: 4096}, 0, 4096, false},
		{"agent output override", config.ModelConfig{ID: "fixture", ContextWindow: 128000, MaxOutputTokens: 4096}, 512, 512, false},
		{"unknown custom output override", config.ModelConfig{ID: "fixture"}, 512, 512, false},
		{"invalid model ceiling", config.ModelConfig{ID: "fixture", ContextWindow: 128000, MaxOutputTokens: 128000}, 0, 0, true},
		{"negative override", config.ModelConfig{ID: "fixture"}, -1, 0, true},
		{"oversized override", config.ModelConfig{ID: "fixture", ContextWindow: 128000, MaxOutputTokens: 4096}, 8192, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				var input map[string]any
				if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
					t.Error(err)
					http.Error(w, "bad JSON", 400)
					return
				}
				if tc.want == 0 {
					if _, ok := input["max_tokens"]; ok {
						t.Errorf("legacy context leaked to output: %v", input["max_tokens"])
					}
				} else if input["max_tokens"] != float64(tc.want) {
					t.Errorf("wire max_tokens=%v want=%d", input["max_tokens"], tc.want)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"id":"fixture","choices":[{"message":{"role":"assistant","content":"answer"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2}}`))
			}))
			defer fixture.Close()
			database, _ := testpg.Open(t)
			store, err := session.NewPostgresStore(database)
			require.NoError(t, err)
			cfg := config.Default()
			cfg.Agent.DefaultProvider = "fixture"
			cfg.Agent.DefaultModel = "fixture"
			cfg.Agent.MaxOutputTokens = tc.override
			cfg.Rewriter.Enabled = false
			cfg.Providers = []config.ProviderConfig{{Name: "fixture", Type: "openai", APIKey: "test-only", BaseURL: fixture.URL, Models: []config.ModelConfig{tc.model}}}
			s := &Server{cfg: cfg, sessionStore: store, toolRegistry: tools.NewRegistry()}
			sess := session.NewSession(t.TempDir())
			require.NoError(t, store.Create(context.Background(), sess))
			a, err := s.createAgent(t.Context(), sess.ID)
			if tc.invalid {
				require.ErrorContains(t, err, "token limits")
				require.Zero(t, calls.Load())
				return
			}
			require.NoError(t, err)
			defer a.Shutdown()
			_, err = a.Run(t.Context(), "Please answer briefly")
			require.NoError(t, err)
			require.EqualValues(t, 1, calls.Load())
		})
	}
}

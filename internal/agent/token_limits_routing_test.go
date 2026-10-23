package agent

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tokenizer"
	"github.com/stretchr/testify/require"
)

func TestModelChangesResolveLimitsForActualHTTPRequests(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(fmt.Sprint("stream=", streaming), func(t *testing.T) {
			var calls atomic.Int32
			fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				var input struct {
					Model     string `json:"model"`
					MaxTokens int    `json:"max_tokens"`
					Stream    bool   `json:"stream"`
				}
				require.NoError(t, json.NewDecoder(r.Body).Decode(&input))
				want := 4096
				if input.Model == "small" {
					want = 1024
				}
				if input.MaxTokens != want {
					t.Errorf("model %s output=%d want=%d", input.Model, input.MaxTokens, want)
				}
				if input.Stream {
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"answer\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
					return
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"id":"fixture","choices":[{"message":{"role":"assistant","content":"answer"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2}}`)
			}))
			defer fixture.Close()
			models := []providers.Model{{ID: "large", ContextWindow: 128000, DefaultMaxTokens: 4096}, {ID: "small", ContextWindow: 32000, DefaultMaxTokens: 1024}}
			p, err := providers.NewProvider(providers.FactoryConfig{Type: "openai", APIKey: "test-only", BaseURL: fixture.URL, Models: models})
			require.NoError(t, err)
			a := New(Config{Provider: p, Model: "large", ContextWindow: 128000, MaxTokens: 4096, TokenLimits: providers.TokenLimitsFor(models, 0), Tokenizer: tokenizer.NewEstimator()})
			defer a.Shutdown()
			run := func() {
				if streaming {
					events, err := a.Stream(t.Context(), "answer briefly")
					require.NoError(t, err)
					for e := range events {
						require.NoError(t, e.Error)
					}
				} else {
					_, err := a.Run(t.Context(), "answer briefly")
					require.NoError(t, err)
				}
			}
			run()
			a.SetModel("small")
			run()
			require.Equal(t, 32000, a.BudgetReport().MaxTokens)
			require.EqualValues(t, 2, calls.Load())
			// Variants resolve against each requested model as well, instead of inheriting
			// whichever model ran last in the main conversation.
			results, err := a.RunVariants(t.Context(), "answer briefly", []string{"large", "small"})
			require.NoError(t, err)
			for _, r := range results {
				require.Empty(t, r.Err)
			}
			require.EqualValues(t, 4, calls.Load())
		})
	}
}

func TestInvalidRoutedOutputOverrideStopsBeforeHTTP(t *testing.T) {
	var calls atomic.Int32
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); http.Error(w, "must not call", 500) }))
	defer fixture.Close()
	models := []providers.Model{{ID: "large", ContextWindow: 128000, DefaultMaxTokens: 4096}, {ID: "small", ContextWindow: 32000, DefaultMaxTokens: 1024}}
	p, err := providers.NewProvider(providers.FactoryConfig{Type: "openai", APIKey: "test-only", BaseURL: fixture.URL, Models: models})
	require.NoError(t, err)
	a := New(Config{Provider: p, Model: "large", MaxTokens: 2048, ContextWindow: 128000, TokenLimits: providers.TokenLimitsFor(models, 2048), Tokenizer: tokenizer.NewEstimator()})
	defer a.Shutdown()
	a.SetModel("small")
	_, err = a.Run(t.Context(), "answer briefly")
	require.ErrorContains(t, err, "exceeds model output limit")
	require.Zero(t, calls.Load())
}

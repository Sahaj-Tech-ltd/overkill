package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/subagent"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// Run the complete interactive CLI assembly in a subprocess so process-global
// stdin, signals, background finalizers and config cannot leak into other tests.
func TestCLIOutputLimitsHelper(t *testing.T) {
	if os.Getenv("OVERKILL_TOKEN_LIMIT_TEST_HELPER") != "1" {
		return
	}
	cfg = config.Default()
	cfg.DatabaseURL = ""
	cfg.Rewriter.Enabled = false
	cfg.Agent.MaxTurns = 1
	cfg.Agent.MaxOutputTokens = 512
	cfg.Agent.DefaultProvider = "fixture"
	cfg.Agent.DefaultModel = "fixture"
	cfg.Providers = []config.ProviderConfig{{Name: "fixture", Type: "openai", APIKey: "test-only", BaseURL: os.Getenv("OVERKILL_TOKEN_FIXTURE"), Models: []config.ModelConfig{{ID: "fixture", MaxTokens: 200000, MaxOutputTokens: 4096}}}}
	noPersonality = true
	command := &cobra.Command{}
	command.SetContext(context.Background())
	require.NoError(t, runAgent(command, nil))
}

func TestCLIOutputLimitsHTTPPayload(t *testing.T) {
	var calls atomic.Int32
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var input map[string]any
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
			http.Error(w, "bad JSON", 400)
			return
		}
		if input["max_tokens"] != float64(512) {
			t.Errorf("CLI wire output=%v, want 512; context must not leak", input["max_tokens"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"fixture","choices":[{"message":{"role":"assistant","content":"answer"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2}}`))
	}))
	defer fixture.Close()
	command := exec.Command(os.Args[0], "-test.run=^TestCLIOutputLimitsHelper$")
	command.Dir = t.TempDir()
	command.Env = append(os.Environ(), "OVERKILL_TOKEN_LIMIT_TEST_HELPER=1", "OVERKILL_TOKEN_FIXTURE="+fixture.URL, "OVERKILL_HOME="+t.TempDir(), "DATABASE_URL=", "OVERKILL_NO_HALLUSCAN=1")
	command.Stdin = strings.NewReader("Please answer two plus two briefly.\n/quit\n")
	output, err := command.CombinedOutput()
	require.NoError(t, err, string(output))
	require.EqualValues(t, 1, calls.Load(), string(output))
}

func TestChildOutputLimitsHTTPPayload(t *testing.T) {
	for _, tc := range []struct {
		name                string
		parent, child, want int
		invalid             bool
	}{
		{"model default", 0, 0, 4096, false}, {"parent default", 512, 0, 512, false}, {"task override", 512, 256, 256, false}, {"negative task override", 0, -1, 0, true}, {"above model cap", 0, 8192, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				var input map[string]any
				_ = json.NewDecoder(r.Body).Decode(&input)
				if input["max_tokens"] != float64(tc.want) {
					t.Errorf("child wire output=%v want=%d", input["max_tokens"], tc.want)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"id":"fixture","choices":[{"message":{"role":"assistant","content":"answer"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2}}`))
			}))
			defer fixture.Close()
			conf := config.Default()
			conf.Agent.DefaultProvider = "fixture"
			conf.Agent.DefaultModel = "fixture"
			conf.Agent.MaxOutputTokens = tc.parent
			conf.Providers = []config.ProviderConfig{{Name: "fixture", Type: "openai", APIKey: "test-only", BaseURL: fixture.URL, Models: []config.ModelConfig{{ID: "fixture", MaxTokens: 200000, MaxOutputTokens: 4096}}}}
			worker, err := childWorkerFactory(conf, t.TempDir())(subagent.RealWorkerConfig{Goal: "answer briefly", MaxTokens: tc.child, MaxSteps: 1})
			if tc.invalid {
				require.ErrorContains(t, err, "token limits")
				require.Zero(t, calls.Load())
				return
			}
			require.NoError(t, err)
			_, err = worker.Run(t.Context())
			require.NoError(t, err)
			require.EqualValues(t, 1, calls.Load())
		})
	}
}

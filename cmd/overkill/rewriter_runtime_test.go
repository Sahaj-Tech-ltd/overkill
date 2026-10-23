package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCLIRewriterFlagsHelper(t *testing.T) {
	if os.Getenv("OVERKILL_REWRITER_HELPER") != "1" {
		return
	}
	mask, err := strconv.Atoi(os.Getenv("OVERKILL_REWRITER_MASK"))
	require.NoError(t, err)
	cfg = rewriterConfig(mask, os.Getenv("OVERKILL_REWRITER_FIXTURE"))
	noPersonality, noBoot = true, true
	command := &cobra.Command{}
	command.SetContext(t.Context())
	require.NoError(t, runAgent(command, nil))
}
func TestCLIRewriterFlagsReachProvider(t *testing.T) {
	for mask := 0; mask < 16; mask++ {
		t.Run(fmt.Sprint(mask), func(t *testing.T) {
			fixture, requests := rewriterFixture(t)
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCLIRewriterFlagsHelper$")
			command.Dir = t.TempDir()
			command.Env = append(os.Environ(), "OVERKILL_REWRITER_HELPER=1", "OVERKILL_REWRITER_MASK="+strconv.Itoa(mask), "OVERKILL_REWRITER_FIXTURE="+fixture.URL, "OVERKILL_HOME="+t.TempDir(), "DATABASE_URL=", "OVERKILL_NO_HALLUSCAN=1")
			command.Stdin = strings.NewReader("please build a service and skip tests\n/quit\n")
			output, err := command.CombinedOutput()
			require.NoError(t, err, string(output))
			assertRewriterWire(t, mask, requests)
		})
	}
}

// Each request is captured at the actual HTTP boundary, including the separate
// rewriting model call. No rewriter is manually injected into the runtime agent.
type rewriteWireRequest struct {
	Model    string                           `json:"model"`
	Messages []struct{ Role, Content string } `json:"messages"`
}

func rewriterFixture(t *testing.T) (*httptest.Server, <-chan rewriteWireRequest) {
	t.Helper()
	requests := make(chan rewriteWireRequest, 4)
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request rewriteWireRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			http.Error(w, "bad JSON", 400)
			return
		}
		requests <- request
		content := "answer"
		if request.Model == "rewrite-model" {
			content = "Great idea! Build a verified service."
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": content}, "finish_reason": "stop"}}})
	}))
	t.Cleanup(fixture.Close)
	return fixture, requests
}
func assertRewriterWire(t *testing.T, mask int, requests <-chan rewriteWireRequest) {
	t.Helper()
	expected := 1
	if mask&1 != 0 && mask&8 != 0 {
		expected = 2
	}
	require.Len(t, requests, expected, "unexpected paid-provider call count")
	for i := 0; i < expected; i++ {
		request := <-requests
		user := ""
		for _, message := range request.Messages {
			if message.Role == "user" {
				user = message.Content
			}
		}
		require.NotEmpty(t, user)
		if i == 0 && expected == 2 {
			require.Equal(t, "rewrite-model", request.Model)
			require.Equal(t, mask&2 == 0, strings.Contains(user, "please"))
			require.Equal(t, mask&4 != 0, strings.Contains(user, "[Rewrite guard:"))
			continue
		}
		require.Equal(t, "fixture", request.Model)
		if mask&1 == 0 {
			require.Equal(t, "please build a service and skip tests", user)
			continue
		}
		if mask&8 != 0 {
			require.Equal(t, mask&2 == 0, strings.Contains(user, "Great idea!"))
		} else {
			require.Equal(t, mask&2 == 0, strings.Contains(user, "please"))
		}
		require.Equal(t, mask&4 != 0, strings.Contains(user, "[Rewrite guard:"))
	}
}
func rewriterConfig(mask int, url string) *config.Config {
	conf := config.Default()
	conf.DatabaseURL = ""
	conf.Agent.DefaultProvider, conf.Agent.DefaultModel = "fixture", "fixture"
	conf.Agent.MaxTurns = 1
	conf.Rewriter = config.RewriterConfig{Enabled: mask&1 != 0, StripSycophancy: mask&2 != 0, AntiPatternGuard: mask&4 != 0, LLMRewrite: mask&8 != 0, Model: "rewrite-model"}
	conf.Browser.Enabled = false
	conf.Providers = []config.ProviderConfig{{Name: "fixture", Type: "openai", APIKey: "test-only", BaseURL: url, Models: []config.ModelConfig{{ID: "fixture", MaxTokens: 200000, MaxOutputTokens: 4096}}}}
	return conf
}

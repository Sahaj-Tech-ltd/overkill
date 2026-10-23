package main

import (
	"context"
	"encoding/json"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/subagent"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func personaFixture(t *testing.T) (*httptest.Server, <-chan string) {
	t.Helper()
	prompts := make(chan string, 16)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
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
		content := "neutral fixture response"
		for _, level := range []string{"subtle", "witty", "full"} {
			if strings.Contains(prompt, "Communication style ("+level+")") {
				content = level + " fixture response"
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": content}, "finish_reason": "stop"}}})
	}))
	t.Cleanup(provider.Close)
	return provider, prompts
}
func personaConfig(level, url string) *config.Config {
	cfg := config.Default()
	cfg.Agent.DefaultProvider, cfg.Agent.DefaultModel = "fixture", "fixture-model"
	cfg.Personality = config.PersonalityConfig{Level: level, Language: "ja"}
	cfg.Rewriter.Enabled = false
	cfg.Browser.Enabled = false
	cfg.Providers = []config.ProviderConfig{{Name: "fixture", Type: "openai", APIKey: "test-only", BaseURL: url, Models: []config.ModelConfig{{ID: "fixture-model", ContextWindow: 32000, MaxOutputTokens: 1024}}}}
	return cfg
}
func assertPersonaPrompt(t *testing.T, prompt, level string) {
	t.Helper()
	require.Contains(t, prompt, `locale "ja"`)
	if level == "none" || level == "off" {
		require.NotContains(t, prompt, "Communication style")
		return
	}
	require.Contains(t, prompt, "Communication style ("+level+")")
	require.Contains(t, prompt, "tool restrictions")
	require.Contains(t, prompt, "requested output format take priority")
}
func TestConfiguredPersonalityPropagatesToActualChildProvider(t *testing.T) {
	for _, level := range []string{"none", "off", "subtle", "witty", "full"} {
		t.Run(level, func(t *testing.T) {
			provider, prompts := personaFixture(t)
			cfg := personaConfig(level, provider.URL)
			factory := childWorkerFactory(cfg, t.TempDir())
			worker, err := factory(subagent.RealWorkerConfig{Goal: "Explain the supplied context without tool use.", MaxSteps: 1})
			require.NoError(t, err)
			result, err := worker.Run(t.Context())
			require.NoError(t, err)
			expected := level + " fixture response"
			if level == "off" || level == "none" {
				expected = "neutral fixture response"
			}
			require.Equal(t, expected, result.Summary)
			prompt := <-prompts
			assertPersonaPrompt(t, prompt, level)
			require.Contains(t, prompt, "Do not claim a filesystem change unless a successful tool invocation made it")
			require.Contains(t, prompt, "using only the tools provided")
		})
	}
}
func TestStandaloneCLIPersonalityLevelsAndFlagReachActualProvider(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	binary := filepath.Join(t.TempDir(), "overkill")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.CommandContext(ctx, filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", binary, ".")
	output, err := build.CombinedOutput()
	require.NoError(t, err, "CLI build: %s", output)
	for _, level := range []string{"none", "off", "subtle", "witty", "full", "flag-disabled"} {
		t.Run(level, func(t *testing.T) {
			provider, prompts := personaFixture(t)
			configured := level
			if level == "flag-disabled" {
				configured = "full"
			}
			cfg := personaConfig(configured, provider.URL)
			cfg.Agent.SystemPrompt = "CLI_HOST_RULES: Accuracy and approval requirements are mandatory."
			home, workspace := t.TempDir(), t.TempDir()
			path := filepath.Join(home, "config.toml")
			require.NoError(t, cfg.Save(path))
			args := []string{"--config", path, "run", "--no-boot", "--output", "jsonl"}
			if level == "flag-disabled" {
				args = append(args, "--no-personality")
			}
			command := exec.CommandContext(ctx, binary, args...)
			command.Env = append(os.Environ(), "OVERKILL_HOME="+home, "DATABASE_URL=", "ANTHROPIC_API_KEY=")
			command.Dir = workspace
			command.Stdin = strings.NewReader("Explain this configuration.\n")
			output, err := command.CombinedOutput()
			require.NoError(t, err, "CLI: %s", output)
			expectedLevel := configured
			if level == "flag-disabled" {
				expectedLevel = "off"
			}
			expected := expectedLevel + " fixture response"
			if expectedLevel == "none" || expectedLevel == "off" {
				expected = "neutral fixture response"
			}
			require.Contains(t, string(output), expected)
			prompt := <-prompts
			assertPersonaPrompt(t, prompt, expectedLevel)
			require.Contains(t, prompt, cfg.Agent.SystemPrompt)
			persisted, err := config.Load(path)
			require.NoError(t, err)
			require.Equal(t, configured, persisted.Personality.Level, "CLI flag must not rewrite configuration")
		})
	}
}

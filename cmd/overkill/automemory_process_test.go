package main

import (
	"context"
	"encoding/json"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestStandaloneCLIAutomaticMemoryPersistsAcrossProcessRestart(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	binary := filepath.Join(t.TempDir(), "overkill")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.CommandContext(ctx, filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", binary, ".")
	output, err := build.CombinedOutput()
	require.NoError(t, err, "CLI build: %s", output)
	home, workspace := t.TempDir(), t.TempDir()
	var mu sync.Mutex
	var prompts []string
	extractions := 0
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct{ Role, Content string } `json:"messages"`
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil {
			http.Error(w, "invalid request", 400)
			return
		}
		prompt := ""
		for _, m := range req.Messages {
			if m.Role == "system" {
				prompt += m.Content
			}
		}
		content := "Memory fixture completed."
		mu.Lock()
		if strings.HasPrefix(prompt, "Extract durable, non-sensitive facts") {
			extractions++
			content = `{"facts":[{"category":"user","content":"User prefers compact diffs."}]}`
		} else {
			prompts = append(prompts, prompt)
		}
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": content}, "finish_reason": "stop"}}})
	}))
	defer fixture.Close()
	cfg := config.Default()
	cfg.Rewriter.Enabled = false
	cfg.Browser.Enabled = false
	cfg.AutoMemory = config.AutoMemoryConfig{Enabled: true, MinNewMessages: 1}
	cfg.Agent.DefaultProvider, cfg.Agent.DefaultModel = "fixture", "fixture-model"
	cfg.Providers = []config.ProviderConfig{{Name: "fixture", Type: "openai", APIKey: "local-test-only", BaseURL: fixture.URL, Models: []config.ModelConfig{{ID: "fixture-model", ContextWindow: 32000, MaxOutputTokens: 1024}}}}
	path := filepath.Join(home, "config.toml")
	require.NoError(t, cfg.Save(path))
	run := func(input string) {
		command := exec.CommandContext(ctx, binary, "--config", path, "run", "--no-personality", "--no-boot", "--output", "jsonl")
		command.Env = append(os.Environ(), "OVERKILL_HOME="+home, "DATABASE_URL=", "ANTHROPIC_API_KEY=")
		command.Dir = workspace
		command.Stdin = strings.NewReader(input + "\n")
		output, err := command.CombinedOutput()
		require.NoError(t, err, "CLI process: %s", output)
		require.Contains(t, string(output), "Memory fixture completed.")
		require.NotContains(t, string(output), "User prefers compact diffs.", "internal extraction must not become a JSONL assistant response")
	}
	run("I prefer compact diffs.")
	run("What format should you use?")
	mu.Lock()
	require.Len(t, prompts, 2)
	require.NotContains(t, prompts[0], "Saved memory facts")
	require.Contains(t, prompts[1], "User prefers compact diffs.")
	require.Equal(t, 2, extractions)
	mu.Unlock()
	files, err := filepath.Glob(filepath.Join(home, "memory", "*", "facts.json"))
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.NoError(t, os.WriteFile(filepath.Join(home, "user.yaml"), []byte("advanced:\n  memory:\n    enabled: false\n"), 0600))
	run("Do not remember this turn.")
	mu.Lock()
	require.Equal(t, 2, extractions)
	require.NotContains(t, prompts[2], "Saved memory facts")
	mu.Unlock()
}

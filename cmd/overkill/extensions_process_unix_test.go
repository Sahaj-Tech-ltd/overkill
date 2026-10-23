//go:build unix

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/extensions"
	"github.com/stretchr/testify/require"
)

func TestActualCLIUnifiedExtensionsInventoryAndPersistentToggle(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "overkill")
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-race", "-o", binary, ".").CombinedOutput()
	require.NoError(t, err, string(output))
	home, workspace := t.TempDir(), t.TempDir()
	skillDir := filepath.Join(home, "skills", "extension-fixture")
	require.NoError(t, os.MkdirAll(skillDir, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: extension-fixture\ndescription: CLI extension acceptance fixture\nenabled: true\n---\nCLI_EXTENSION_SENTINEL: Check workspace criteria.\n"), 0600))
	plugins := filepath.Join(home, "plugins")
	require.NoError(t, os.MkdirAll(plugins, 0700))
	forbidden := filepath.Join(home, "must-not-launch")
	require.NoError(t, os.WriteFile(filepath.Join(plugins, "discovered-plugin"), []byte(fmt.Sprintf("#!/bin/sh\ntouch %q\n", forbidden)), 0700))
	hookDir := filepath.Join(home, "hooks", "after_turn")
	require.NoError(t, os.MkdirAll(hookDir, 0700))
	marker := filepath.Join(home, "after-turn")
	require.NoError(t, os.WriteFile(filepath.Join(hookDir, "completed.sh"), []byte(fmt.Sprintf("#!/bin/sh\nprintf 'hit\\n' >> %q\n", marker)), 0700))
	prompts := make(chan string, 4)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct{ Role, Content string } `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			http.Error(w, "invalid JSON", 400)
			return
		}
		prompt := ""
		for _, message := range body.Messages {
			if message.Role == "system" {
				prompt += message.Content
			}
		}
		prompts <- prompt
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"CLI extension completed"},"finish_reason":"stop"}]}`)
	}))
	defer provider.Close()
	cfg := config.Default()
	cfg.DatabaseURL = ""
	cfg.Rewriter.Enabled = false
	cfg.Agent.DefaultProvider, cfg.Agent.DefaultModel = "fixture", "fixture-model"
	cfg.Providers = []config.ProviderConfig{{Name: "fixture", Type: "openai", APIKey: "fixture-only", BaseURL: provider.URL, Models: []config.ModelConfig{{ID: "fixture-model", ContextWindow: 32000, MaxOutputTokens: 1024}}}}
	cfg.MCP.Servers = []config.MCPServer{{Name: "configured-mcp", Command: "must-not-launch", Env: map[string]string{"SECRET": "CLI-PRIVATE-SECRET"}}}
	configPath := filepath.Join(home, "config.toml")
	require.NoError(t, cfg.Save(configPath))
	invoke := func(input string, args ...string) ([]byte, error) {
		command := exec.CommandContext(ctx, binary, append([]string{"--config", configPath}, args...)...)
		command.Env = append(os.Environ(), "OVERKILL_HOME="+home, "DATABASE_URL=", "OVERKILL_NO_HALLUSCAN=1")
		command.Dir = workspace
		command.Stdin = strings.NewReader(input)
		return command.Output()
	}
	output, err = invoke("", "extensions", "--json")
	require.NoError(t, err, string(output))
	require.NotContains(t, string(output), "CLI-PRIVATE-SECRET")
	var items []extensions.Extension
	require.NoError(t, json.Unmarshal(output, &items))
	found := map[extensions.Kind]bool{}
	for _, item := range items {
		found[item.Kind] = true
		if item.Kind != extensions.KindSkill {
			require.False(t, item.Enabled)
			require.False(t, item.CanToggle)
		}
	}
	for _, kind := range []extensions.Kind{extensions.KindSkill, extensions.KindHook, extensions.KindPlugin, extensions.KindMCP} {
		require.True(t, found[kind], "actual CLI missing %s", kind)
	}
	_, err = invoke("", "extensions", "disable", "skill", "extension-fixture")
	require.NoError(t, err)
	output, err = invoke("Check workspace criteria.\n/quit\n", "run", "--no-personality", "--no-boot")
	require.NoError(t, err, string(output))
	require.NotContains(t, <-prompts, "CLI_EXTENSION_SENTINEL")
	_, err = invoke("", "extensions", "enable", "skill", "extension-fixture")
	require.NoError(t, err)
	output, err = invoke("Check workspace criteria.\n/quit\n", "run", "--no-personality", "--no-boot")
	require.NoError(t, err, string(output))
	require.Contains(t, <-prompts, "CLI_EXTENSION_SENTINEL")
	data, err := os.ReadFile(marker)
	require.NoError(t, err)
	require.Equal(t, "hit\nhit\n", string(data), "one after_turn hook for each actual CLI turn")
	_, err = invoke("", "extensions", "disable", "plugin", "discovered-plugin")
	require.Error(t, err)
	_, err = os.Stat(forbidden)
	require.ErrorIs(t, err, os.ErrNotExist)
}

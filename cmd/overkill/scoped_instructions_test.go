package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/instructions"
	"github.com/Sahaj-Tech-ltd/overkill/internal/subagent"
	"github.com/stretchr/testify/require"
)

func TestStandaloneCLIScopedInstructionsAndChildFactory(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	binary := filepath.Join(t.TempDir(), "overkill")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.CommandContext(ctx, filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", binary, ".")
	output, err := build.CombinedOutput()
	require.NoError(t, err, "CLI build: %s", output)
	provider, prompts := personaFixture(t)
	cfg := personaConfig("off", provider.URL)
	home, workspace := t.TempDir(), t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(workspace, "nested"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(workspace, "AGENTS.md"), []byte("cli-root-guidance"), 0600))
	path := filepath.Join(workspace, "nested", "AGENTS.md")
	require.NoError(t, os.WriteFile(path, []byte("cli-nested-guidance"), 0600))
	global := filepath.Join(home, "global.md")
	require.NoError(t, os.WriteFile(global, []byte("cli-global-guidance"), 0600))
	cfg.Agent.Instructions = instructions.Config{Enabled: true, GlobalFile: global, Directory: "nested"}
	configPath := filepath.Join(home, "config.toml")
	require.NoError(t, cfg.Save(configPath))
	command := exec.CommandContext(ctx, binary, "--config", configPath, "run", "--no-boot", "--output", "jsonl")
	command.Env = append(os.Environ(), "OVERKILL_HOME="+home, "DATABASE_URL=", "ANTHROPIC_API_KEY=")
	command.Dir = workspace
	command.Stdin = strings.NewReader("Inspect this workspace.\n")
	output, err = command.CombinedOutput()
	require.NoError(t, err, "CLI: %s", output)
	prompt := <-prompts
	require.Contains(t, prompt, "cli-global-guidance")
	require.Contains(t, prompt, "cli-root-guidance")
	require.Contains(t, prompt, "cli-nested-guidance")
	// The production child factory must carry the same explicit configuration.
	worker, err := childWorkerFactory(cfg, workspace)(subagent.RealWorkerConfig{Goal: "Explain supplied context", MaxSteps: 1})
	require.NoError(t, err)
	_, err = worker.Run(t.Context())
	require.NoError(t, err)
	prompt = <-prompts
	require.Contains(t, prompt, "cli-global-guidance")
	require.Contains(t, prompt, "cli-root-guidance")
	require.Contains(t, prompt, "cli-nested-guidance")
}

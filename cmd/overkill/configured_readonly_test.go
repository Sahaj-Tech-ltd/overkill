package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/subagent"
	"github.com/stretchr/testify/require"
)

func TestConfiguredReadonlyCLILoadAndChildInheritance(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	var calls atomic.Int32
	var mentionObserved atomic.Bool
	feedback := make(chan string, 2)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct{ Role, Content string } `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if calls.Add(1)%2 == 1 {
			for _, m := range body.Messages {
				if strings.Contains(m.Content, "CLI-SELECTED-CONTEXT") {
					mentionObserved.Store(true)
				}
			}
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"write","type":"function","function":{"name":"fs","arguments":"{\"action\":\"write\",\"path\":\"proof.txt\",\"content\":\"changed\"}"}}]},"finish_reason":"tool_calls"}]}`))
			return
		}
		text := ""
		for _, m := range body.Messages {
			if m.Role == "tool" {
				text += m.Content
			}
		}
		feedback <- text
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"Write denial observed."},"finish_reason":"stop"}]}`))
	}))
	defer provider.Close()
	cfg := personaConfig("off", provider.URL)
	cfg.Security.AutonomyLevel = "readonly"
	home, workspace := t.TempDir(), t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(workspace, "context.txt"), []byte("CLI-SELECTED-CONTEXT"), 0600))
	path := filepath.Join(home, "config.toml")
	require.NoError(t, cfg.Save(path))
	binary := filepath.Join(t.TempDir(), "overkill")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.CommandContext(ctx, filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", binary, ".")
	output, err := build.CombinedOutput()
	require.NoError(t, err, "build: %s", output)
	command := exec.CommandContext(ctx, binary, "--config", path, "run", "--no-personality", "--no-boot", "--output", "jsonl")
	command.Env = append(os.Environ(), "OVERKILL_HOME="+home, "DATABASE_URL=", "ANTHROPIC_API_KEY=")
	command.Dir = workspace
	command.Stdin = strings.NewReader("Try writing proof.txt and inspect @context.txt\n")
	output, err = command.CombinedOutput()
	require.NoError(t, err, "CLI: %s", output)
	require.Contains(t, <-feedback, "configured security.autonomy_level=readonly")
	require.True(t, mentionObserved.Load(), "actual CLI mention must use its workspace")
	_, err = os.Stat(filepath.Join(workspace, "proof.txt"))
	require.True(t, os.IsNotExist(err))
	worker, err := childWorkerFactory(cfg, workspace)(subagent.RealWorkerConfig{Goal: "Try writing proof.txt", Tools: []string{"fs"}, MaxSteps: 2})
	require.NoError(t, err)
	result, err := worker.Run(ctx)
	require.Error(t, err, "denied child cannot claim verified workspace work")
	require.Empty(t, result.FilesWritten)
	require.Equal(t, "error", result.Status)
	require.Contains(t, <-feedback, "configured security.autonomy_level=readonly")
	_, err = os.Stat(filepath.Join(workspace, "proof.txt"))
	require.True(t, os.IsNotExist(err))
	require.EqualValues(t, 4, calls.Load())
}

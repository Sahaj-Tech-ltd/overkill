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

	"github.com/Sahaj-Tech-ltd/overkill/internal/acp"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/stretchr/testify/require"
)

func TestActualCLIHotReloadsAtomicConfigReplacements(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	database, _ := testpg.Open(t)
	var schema string
	require.NoError(t, database.QueryRowContext(ctx, `SELECT current_schema()`).Scan(&schema))
	dsn := acpProcessSchemaDSN(t, schema)
	home, workspace := t.TempDir(), t.TempDir()
	store, err := session.NewPostgresStore(database)
	require.NoError(t, err)
	sess := session.NewSession(workspace)
	sess.Provider = "fixture"
	sess.Model = "before"
	require.NoError(t, store.Create(ctx, sess))
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			http.Error(w, "bad request", 400)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		chunk, _ := json.Marshal(map[string]any{"id": "reload-fixture", "choices": []any{map[string]any{"index": 0, "delta": map[string]string{"content": input.Model}, "finish_reason": "stop"}}, "usage": map[string]int{"prompt_tokens": 1, "completion_tokens": 1}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
	}))
	defer provider.Close()
	cfg := config.Default()
	cfg.Rewriter.Enabled = false
	cfg.Browser.Enabled = false
	cfg.Slack.Enabled = false
	cfg.Agent.DefaultProvider = "fixture"
	cfg.Agent.DefaultModel = "before"
	cfg.DatabaseURL = dsn
	cfg.ACP.Listen = "127.0.0.1:0"
	cfg.ACP.Token = "reload-test-token"
	cfg.Providers = []config.ProviderConfig{{Name: "fixture", Type: "openai", APIKey: "reload-test-key", BaseURL: provider.URL, Models: []config.ModelConfig{{ID: "before", ContextWindow: 32000, MaxOutputTokens: 100}, {ID: "after", ContextWindow: 32000, MaxOutputTokens: 100}}}}
	configPath := filepath.Join(home, "config.toml")
	require.NoError(t, cfg.Save(configPath))
	userPath := filepath.Join(home, "user.yaml")
	require.NoError(t, os.WriteFile(userPath, []byte("schema_version: 1\nbasic:\n  model: before\n"), 0600))
	binary := filepath.Join(t.TempDir(), "overkill")
	build := exec.CommandContext(ctx, filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", binary, ".")
	output, err := build.CombinedOutput()
	require.NoError(t, err, "build actual executable: %s", output)
	process := startACPCLIProcess(t, ctx, binary, configPath, home, workspace, dsn, []string{"reload-test-token", "reload-test-key"})
	process.awaitReady(t, ctx)
	client := acp.NewClient("http://"+process.address, "reload-test-token")
	turn := func() string {
		events, err := client.SendTo(ctx, sess.ID, "report model")
		require.NoError(t, err)
		var result strings.Builder
		for event := range events {
			require.NotEqual(t, "error", event.Type, event.Error)
			if event.Type == "text_delta" {
				result.WriteString(event.Content)
			}
		}
		return result.String()
	}
	require.Equal(t, "before", turn())
	for _, model := range []string{"after", "before", "after"} {
		replacement := filepath.Join(home, "atomic-save")
		require.NoError(t, os.WriteFile(replacement, []byte("schema_version: 1\nbasic:\n  model: "+model+"\n"), 0600))
		require.NoError(t, os.Rename(replacement, userPath))
		deadline := time.Now().Add(5 * time.Second)
		for {
			if turn() == model {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("running agent did not reload %s", model)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	require.NoError(t, process.stop())
}

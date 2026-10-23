//go:build unix

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
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
	syncpkg "github.com/Sahaj-Tech-ltd/overkill/internal/sync"
	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/stretchr/testify/require"
)

func TestActualCLISyncAutoPushPersistsCompletedTurns(t *testing.T) {
	database, _ := testpg.Open(t)
	var schema string
	require.NoError(t, database.QueryRowContext(t.Context(), `SELECT current_schema()`).Scan(&schema))
	binary := filepath.Join(t.TempDir(), "overkill")
	buildCtx, buildCancel := context.WithTimeout(t.Context(), 90*time.Second)
	output, err := exec.CommandContext(buildCtx, filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-race", "-o", binary, ".").CombinedOutput()
	buildCancel()
	require.NoError(t, err, string(output))
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []struct{ Role, Content string } `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			http.Error(w, "invalid JSON", 400)
			return
		}
		input := ""
		for _, message := range request.Messages {
			if message.Role == "user" {
				input = message.Content
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "sync-fixture-answer: " + input}, "finish_reason": "stop"}}})
	}))
	defer fixture.Close()
	for _, enabled := range []bool{true, false} {
		t.Run(map[bool]string{true: "enabled", false: "disabled"}[enabled], func(t *testing.T) {
			home, workspace, remote := t.TempDir(), t.TempDir(), t.TempDir()
			conf := config.Default()
			conf.Rewriter.Enabled, conf.Browser.Enabled = false, false
			conf.Agent.DefaultProvider, conf.Agent.DefaultModel, conf.Agent.MaxTurns = "fixture", "fixture-model", 1
			conf.DatabaseURL = acpProcessSchemaDSN(t, schema)
			conf.Providers = []config.ProviderConfig{{Name: "fixture", Type: "openai", APIKey: "fixture-only", BaseURL: fixture.URL, Models: []config.ModelConfig{{ID: "fixture-model", ContextWindow: 32000, MaxOutputTokens: 1024}}}}
			conf.Sync = config.SyncConfig{AutoPush: enabled, Backend: "file", File: config.SyncFileConfig{Path: remote}, PushTimeoutSec: 5}
			path := filepath.Join(home, "config.toml")
			require.NoError(t, conf.Save(path))
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, binary, "--config", path, "run", "--no-personality", "--no-boot")
			command.Env = append(os.Environ(), "OVERKILL_HOME="+home, "OVERKILL_NO_HALLUSCAN=1")
			command.Dir = workspace
			var output bytes.Buffer
			command.Stdout, command.Stderr = &output, &output
			stdin, err := command.StdinPipe()
			require.NoError(t, err)
			require.NoError(t, command.Start())
			joined := false
			defer func() {
				if !joined {
					cancel()
					_ = command.Wait()
				}
			}()
			backend, err := syncpkg.NewFileBackend(conf.Sync.File)
			require.NoError(t, err)
			_, err = io.WriteString(stdin, "Complete the first sync fixture turn.\n")
			require.NoError(t, err)
			if enabled {
				require.Eventually(t, func() bool {
					entries, err := backend.List(t.Context())
					if err != nil || len(entries) != 1 {
						return false
					}
					data, _, err := backend.Pull(t.Context(), entries[0].ID)
					if err != nil {
						return false
					}
					session, err := syncpkg.DecodeSession(data)
					if err != nil {
						return false
					}
					for _, message := range session.Messages {
						if message.Role == "assistant" && strings.Contains(message.Content, "first sync fixture turn") {
							return true
						}
					}
					return false
				}, 15*time.Second, 50*time.Millisecond, "a completed turn must reach remote while the CLI remains open")
			}
			_, err = io.WriteString(stdin, "Complete the final sync fixture turn.\n/quit\n")
			require.NoError(t, err)
			require.NoError(t, stdin.Close())
			err = command.Wait()
			joined = true
			require.NoError(t, err, output.String())
			require.NotContains(t, output.String(), "DATA RACE")
			entries, err := backend.List(t.Context())
			require.NoError(t, err)
			if !enabled {
				require.Empty(t, entries)
				return
			}
			require.Len(t, entries, 1)
			data, meta, err := backend.Pull(t.Context(), entries[0].ID)
			require.NoError(t, err)
			session, err := syncpkg.DecodeSession(data)
			require.NoError(t, err)
			require.Equal(t, session.ID, meta.ID)
			require.Len(t, session.Messages, 4)
			require.Equal(t, 4, meta.MessageCount)
			require.Contains(t, session.Messages[3].Content, "final sync fixture turn")
		})
	}
	for _, tc := range []struct {
		name, backend, database, want string
	}{
		{"missing database", "file", "", "requires a persistent session database"},
		{"missing backend", "", acpProcessSchemaDSN(t, schema), "requires a configured backend"},
		{"unknown backend", "missing-backend", acpProcessSchemaDSN(t, schema), "unknown backend"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			conf := config.Default()
			conf.Agent.DefaultProvider, conf.Agent.DefaultModel = "fixture", "fixture-model"
			conf.DatabaseURL = tc.database
			conf.Providers = []config.ProviderConfig{{Name: "fixture", Type: "openai", APIKey: "fixture-only", BaseURL: fixture.URL, Models: []config.ModelConfig{{ID: "fixture-model", ContextWindow: 32000, MaxOutputTokens: 1024}}}}
			conf.Sync = config.SyncConfig{AutoPush: true, Backend: tc.backend, File: config.SyncFileConfig{Path: t.TempDir()}}
			path := filepath.Join(home, "config.toml")
			require.NoError(t, conf.Save(path))
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, binary, "--config", path, "run", "--no-personality", "--no-boot")
			command.Env = append(os.Environ(), "OVERKILL_HOME="+home, "DATABASE_URL=")
			command.Dir = t.TempDir()
			command.Stdin = strings.NewReader("This must not silently run without sync.\n")
			output, err := command.CombinedOutput()
			require.Error(t, err)
			require.Contains(t, string(output), tc.want)
		})
	}
}

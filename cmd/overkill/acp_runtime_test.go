package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Sahaj-Tech-ltd/overkill/internal/hooks"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/acp"
	"github.com/Sahaj-Tech-ltd/overkill/internal/api"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/stretchr/testify/require"
)

func TestTwoACPInstancesExecuteWorkspaceToolsThroughSharedRuntime(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Stream   bool                             `json:"stream"`
			Messages []struct{ Role, Content string } `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
			http.Error(w, "invalid JSON", 400)
			return
		}
		if !input.Stream {
			http.Error(w, "unexpected non-stream request", 400)
			return
		}
		user := ""
		hasTool := false
		for _, message := range input.Messages {
			if message.Role == "user" {
				user = message.Content
			}
			if message.Role == "tool" {
				hasTool = true
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		var delta any
		finish := "stop"
		if !hasTool {
			args, _ := json.Marshal(map[string]string{"action": "write", "path": "acp-result.txt", "content": user})
			delta = map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "write", "type": "function", "function": map[string]string{"name": "fs", "arguments": string(args)}}}}
			finish = "tool_calls"
		} else {
			delta = map[string]string{"content": "workspace task completed"}
		}
		chunk, _ := json.Marshal(map[string]any{"id": "fixture", "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}, "usage": map[string]int{"prompt_tokens": 3, "completion_tokens": 2}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
	}))
	t.Cleanup(provider.Close)
	for _, name := range []string{"frontend", "backend"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			database, _ := testpg.Open(t)
			store, err := session.NewPostgresStore(database)
			require.NoError(t, err)
			workspace := t.TempDir()
			cfg := config.Default()
			cfg.Rewriter.Enabled = false
			cfg.Agent.DefaultProvider, cfg.Agent.DefaultModel = "fixture", "fixture-model"
			cfg.Security.AutonomyLevel = "yolo"
			cfg.Providers = []config.ProviderConfig{{Name: "fixture", Type: "openai", APIKey: "test-only", BaseURL: provider.URL, Models: []config.ModelConfig{{ID: "fixture-model", MaxTokens: 32000}}}}
			home := t.TempDir()
			capture := filepath.Join(home, "hooks", "captured.jsonl")
			if runtime.GOOS != "windows" {
				for _, point := range []hooks.HookPoint{hooks.BeforeToolCall, hooks.AfterToolCall} {
					directory := filepath.Join(home, "hooks", string(point))
					require.NoError(t, os.MkdirAll(directory, 0700))
					body := "#!/bin/sh\ncapture=\"$(dirname \"$0\")/../captured.jsonl\"\ncat >> \"$capture\"\nprintf '\\n' >> \"$capture\"\n"
					require.NoError(t, os.WriteFile(filepath.Join(directory, "capture.sh"), []byte(body), 0700))
				}
				// Executable workspace hooks must never be auto-discovered.
				directory := filepath.Join(workspace, "hooks", string(hooks.BeforeToolCall))
				require.NoError(t, os.MkdirAll(directory, 0700))
				require.NoError(t, os.WriteFile(filepath.Join(directory, "untrusted.sh"), []byte("#!/bin/sh\ntouch \"$(dirname \"$0\")/../../UNTRUSTED-RAN\"\n"), 0700))
			}
			services, err := newRuntimeServices(cfg, database, home)
			require.NoError(t, err)
			sess := session.NewSession(workspace)
			sess.Title, sess.Model, sess.Provider = name, "fixture-model", "fixture"
			require.NoError(t, store.Create(t.Context(), sess))
			service := api.NewServer(services.APIConfig(store))
			defer service.Close()
			server := acp.NewServer(acp.Config{Addr: "127.0.0.1:0", Token: "fixture-token", Store: store, ResolveSender: service.ResolveACPSender})
			require.NoError(t, server.Start())
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				require.NoError(t, server.Shutdown(ctx))
			}()
			client := acp.NewClient("http://"+server.Addr(), "fixture-token")
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			events, err := client.SendTo(ctx, sess.ID, "create "+name+" workspace output")
			require.NoError(t, err)
			completed := false
			for event := range events {
				require.NotEqual(t, "error", event.Type, "%s", event.Error)
				if event.Type == "done" {
					completed = true
				}
			}
			require.True(t, completed)
			data, err := os.ReadFile(filepath.Join(workspace, "acp-result.txt"))
			require.NoError(t, err)
			require.Equal(t, "create "+name+" workspace output", string(data))
			if runtime.GOOS != "windows" {
				captured, err := os.ReadFile(capture)
				require.NoError(t, err)
				lines := strings.Split(strings.TrimSpace(string(captured)), "\n")
				require.Len(t, lines, 2, "real scripts must observe one before/after pair")
				for i, line := range lines {
					var event hooks.Event
					require.NoError(t, json.Unmarshal([]byte(line), &event))
					require.Equal(t, sess.ID, event.SessionID)
					require.Equal(t, "fs", event.ToolName)
					require.Equal(t, []hooks.HookPoint{hooks.BeforeToolCall, hooks.AfterToolCall}[i], event.Point)
					require.Contains(t, string(event.ToolInput), "acp-result.txt")
					if i == 1 {
						require.NotEmpty(t, event.ToolOutput)
					}
				}
				_, err = os.Stat(filepath.Join(workspace, "UNTRUSTED-RAN"))
				require.ErrorIs(t, err, os.ErrNotExist)
			}
			persisted, err := store.Load(ctx, sess.ID)
			require.NoError(t, err)
			require.GreaterOrEqual(t, len(persisted.Messages), 4, "tool-backed conversation must be durable before done")
		})
	}
}

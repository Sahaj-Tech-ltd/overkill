package hotreload

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/security"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tokenizer"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
	"github.com/stretchr/testify/require"
)

// A real watcher and provider drive the actual Run/Stream tool gates. A valid
// YAML scanner edit must not be reported as applied while that gate is intact.
func TestReloadReportsScannerRestartWhileRealToolGateStaysInstalled(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(fmt.Sprint("stream=", streaming), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			workspace := t.TempDir()
			shell := tools.NewShellTool()
			positive, _ := json.Marshal(map[string]any{"command": "echo allowed > allowed.txt", "working_dir": workspace, "timeout_seconds": 5})
			_, err := shell.Execute(ctx, positive)
			require.NoError(t, err)
			_, err = os.Stat(filepath.Join(workspace, "allowed.txt"))
			require.NoError(t, err, "positive control proves native shell can write")
			args, _ := json.Marshal(map[string]any{"command": "echo reload_fixture_blocked > forbidden.txt", "working_dir": workspace, "timeout_seconds": 5})
			modelsSeen := make(chan string, 8)
			var blockedResults atomic.Int32
			fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body struct {
					Model    string `json:"model"`
					Stream   bool   `json:"stream"`
					Messages []struct {
						Role    string          `json:"role"`
						Content json.RawMessage `json:"content"`
					} `json:"messages"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					http.Error(w, "invalid fixture input", 400)
					return
				}
				modelsSeen <- body.Model
				lastUser := -1
				for i, m := range body.Messages {
					if m.Role == "user" {
						lastUser = i
					}
				}
				toolResult := false
				for i, m := range body.Messages {
					if i > lastUser && m.Role == "tool" {
						toolResult = true
						if strings.Contains(string(m.Content), "blocked by security scanner") {
							blockedResults.Add(1)
						}
					}
				}
				message := map[string]any{"role": "assistant"}
				finish := "stop"
				if toolResult {
					message["content"] = "Scanner retained."
				} else {
					call := map[string]any{"id": "reload-command", "type": "function", "function": map[string]string{"name": "shell", "arguments": string(args)}}
					if body.Stream {
						call["index"] = 0
					}
					message["tool_calls"] = []any{call}
					finish = "tool_calls"
				}
				choice := map[string]any{"index": 0, "finish_reason": finish}
				if body.Stream {
					choice["delta"] = message
					w.Header().Set("Content-Type", "text/event-stream")
				} else {
					choice["message"] = message
					w.Header().Set("Content-Type", "application/json")
				}
				payload, _ := json.Marshal(map[string]any{"id": "reload-scanner", "choices": []any{choice}, "usage": map[string]int{"prompt_tokens": 2, "completion_tokens": 1}})
				if body.Stream {
					fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", payload)
				} else {
					_, _ = w.Write(payload)
				}
			}))
			defer fixture.Close()
			models := []providers.Model{{ID: "before", ContextWindow: 32000, DefaultMaxTokens: 100}, {ID: "after", ContextWindow: 32000, DefaultMaxTokens: 100}}
			provider, err := providers.NewProvider(providers.FactoryConfig{Type: "openai", APIKey: "scanner-fixture-only", BaseURL: fixture.URL, Models: models})
			require.NoError(t, err)
			registry := tools.NewRegistry()
			require.NoError(t, registry.Register(shell))
			var invocations atomic.Int32
			registry.SetExecutionGuard(func(ctx context.Context, tool tools.Tool, input json.RawMessage) (json.RawMessage, error) {
				invocations.Add(1)
				return tool.Execute(ctx, input)
			})
			a := agent.New(agent.Config{Provider: provider, Model: "before", ContextWindow: 32000, MaxTokens: 100, MaxSteps: 4, Tokenizer: tokenizer.NewEstimator(), TokenLimits: providers.TokenLimitsFor(models, 0), Tools: registry, Scanners: []security.Scanner{security.NewCommandScanner(security.WithProjectPath(workspace), security.WithExtraDenyPatterns([]string{"reload_fixture_blocked"}))}})
			defer a.Shutdown()
			file := filepath.Join(t.TempDir(), "user.yaml")
			require.NoError(t, os.WriteFile(file, []byte("schema_version: 1\nbasic:\n  model: before\nadvanced:\n  scanners:\n    command:\n      enabled: true\n"), 0600))
			bus := New(Paths{UserConfigFile: file})
			require.NoError(t, bus.Start(ctx))
			defer bus.Stop()
			reporter := make(resultReporter, 8)
			stop, err := WireAgent(ctx, bus, a, file, reporter)
			require.NoError(t, err)
			defer stop()
			run := func(prompt, model string) {
				if streaming {
					events, err := a.Stream(ctx, prompt)
					require.NoError(t, err)
					var text strings.Builder
					for e := range events {
						require.NoError(t, e.Error)
						if e.Type == agent.EventToken {
							text.WriteString(e.Content)
						}
					}
					require.Equal(t, "Scanner retained.", text.String())
				} else {
					result, err := a.Run(ctx, prompt)
					require.NoError(t, err)
					require.Equal(t, "Scanner retained.", result.Response)
				}
				for range 2 {
					select {
					case got := <-modelsSeen:
						require.Equal(t, model, got)
					case <-ctx.Done():
						t.Fatal("provider request missing")
					}
				}
				require.Zero(t, invocations.Load(), "blocked tool must not reach execution guard")
				_, err := os.Stat(filepath.Join(workspace, "forbidden.txt"))
				require.True(t, os.IsNotExist(err), "blocked tool produced file: %v", err)
			}
			run("Run the first fixture.", "before")
			replacement := filepath.Join(filepath.Dir(file), "replacement")
			require.NoError(t, os.WriteFile(replacement, []byte("schema_version: 1\nbasic:\n  model: after\nadvanced:\n  scanners:\n    command:\n      enabled: false\n  persona:\n    directive: unapplied-fixture\n"), 0600))
			require.NoError(t, os.Rename(replacement, file))
			result := awaitReload(t, reporter)
			require.True(t, reflect.DeepEqual([]string{"basic.model"}, result.Applied), "%+v", result)
			require.Equal(t, []string{"advanced.scanners", "advanced.persona"}, result.RestartRequired)
			var restart *RestartRequiredError
			require.ErrorAs(t, result.Err, &restart)
			run("Run the second fixture.", "after")
			require.EqualValues(t, 2, blockedResults.Load(), "provider must receive actual scanner failures before and after reload")
		})
	}
}

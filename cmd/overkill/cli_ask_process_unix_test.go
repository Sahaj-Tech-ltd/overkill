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
	"syscall"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/creack/pty"
	"github.com/stretchr/testify/require"
)

// Uses the actual main/root/config/runtime/provider/tool assembly rather than
// invoking runAgent inside a test process with preconfigured globals.
func TestCLIAskActualStandaloneProcess(t *testing.T) {
	buildCtx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	binary := filepath.Join(t.TempDir(), "overkill")
	build := exec.CommandContext(buildCtx, filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", binary, ".")
	output, err := build.CombinedOutput()
	require.NoError(t, err, "build CLI: %s", output)
	for _, tc := range []struct {
		name, input                string
		terminal, jsonl, cancelled bool
		answer                     string
		index                      int
	}{
		{name: "terminal numbered choice", input: "Please ask me for a color.\n2\n/quit\n", terminal: true, answer: "blue", index: 1},
		{name: "terminal free text", input: "Please ask me for a color.\nsea green\n/quit\n", terminal: true, answer: "sea green", index: -1},
		{name: "terminal cancel", input: "Please ask me for a color.\n/cancel\n/quit\n", terminal: true, cancelled: true, index: -1},
		{name: "headless pipe", input: "Please ask me for a color.\n/quit\n"},
		{name: "jsonl terminal", input: "Please ask me for a color.\n/quit\n", terminal: true, jsonl: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			database, _ := testpg.Open(t)
			var schema string
			require.NoError(t, database.QueryRowContext(t.Context(), `SELECT current_schema()`).Scan(&schema))
			dsn := acpProcessSchemaDSN(t, schema)
			toolResults := make(chan string, 8)
			advertised := make(chan bool, 8)
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var input struct {
					Messages []struct {
						Role    string
						Content json.RawMessage
					} `json:"messages"`
					Tools []struct {
						Function struct {
							Name       string
							Parameters json.RawMessage
						} `json:"function"`
					} `json:"tools"`
				}
				if json.NewDecoder(r.Body).Decode(&input) != nil {
					http.Error(w, "bad request", 400)
					return
				}
				hasTool := false
				for _, msg := range input.Messages {
					if msg.Role == "tool" {
						hasTool = true
						var content string
						_ = json.Unmarshal(msg.Content, &content)
						select {
						case toolResults <- content:
						default:
						}
					}
				}
				var message any = map[string]string{"role": "assistant", "content": "ask fixture completed"}
				finish := "stop"
				if !hasTool && len(input.Tools) > 0 {
					valid := false
					for _, tool := range input.Tools {
						if tool.Function.Name == "ask_user" {
							var params map[string]any
							_ = json.Unmarshal(tool.Function.Parameters, &params)
							_, hasProperties := params["properties"]
							valid = hasProperties
						}
					}
					select {
					case advertised <- valid:
					default:
					}
					message = map[string]any{"role": "assistant", "content": "", "tool_calls": []any{map[string]any{"id": "ask-fixture", "type": "function", "function": map[string]string{"name": "ask_user", "arguments": `{"question":"Fixture color?","choices":["red","blue"]}`}}}}
					finish = "tool_calls"
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": message, "finish_reason": finish}}, "usage": map[string]int{"prompt_tokens": 8, "completion_tokens": 2}})
			}))
			defer provider.Close()
			home, workspace := t.TempDir(), t.TempDir()
			// Owner-home scripts prove standalone runtime hook loading, without
			// executing anything from the agent workspace.
			hookCaptures := map[string]string{}
			for _, point := range []string{"before_tool_call", "after_tool_call"} {
				hookDir := filepath.Join(home, "hooks", point)
				require.NoError(t, os.MkdirAll(hookDir, 0700))
				script := filepath.Join(hookDir, "capture.sh")
				require.NoError(t, os.WriteFile(script, []byte("#!/bin/sh\nexec /bin/cat > \"$0.event.json\"\n"), 0700))
				hookCaptures[point] = script + ".event.json"
			}
			c := config.Default()
			c.Rewriter.Enabled = false
			c.Browser.Enabled = false
			c.Slack.Enabled = false
			c.Agent.DefaultProvider = "fixture"
			c.Agent.DefaultModel = "fixture-model"
			c.Agent.MaxTurns = 4
			c.DatabaseURL = dsn
			c.Providers = []config.ProviderConfig{{Name: "fixture", Type: "openai", APIKey: "local-ask-fixture", BaseURL: provider.URL, Models: []config.ModelConfig{{ID: "fixture-model", ContextWindow: 32000, MaxOutputTokens: 1024}}}}
			configPath := filepath.Join(home, "config.toml")
			require.NoError(t, c.Save(configPath))
			ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
			defer cancel()
			args := []string{"--config", configPath, "run", "--no-personality", "--no-boot"}
			if tc.jsonl {
				args = append(args, "--output", "jsonl")
			}
			command := exec.CommandContext(ctx, binary, args...)
			command.Dir = workspace
			command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			for _, key := range []string{"PATH", "HOME", "TMPDIR", "TMP", "TEMP", "LANG", "LC_ALL"} {
				if val, ok := os.LookupEnv(key); ok {
					command.Env = append(command.Env, key+"="+val)
				}
			}
			command.Env = append(command.Env, "OVERKILL_HOME="+home, "DATABASE_URL="+dsn, "OVERKILL_NO_HALLUSCAN=1")
			if tc.terminal {
				master, slave, err := pty.Open()
				require.NoError(t, err)
				defer master.Close()
				defer slave.Close()
				command.Stdin = slave
				_, err = master.Write([]byte(tc.input))
				require.NoError(t, err)
			} else {
				command.Stdin = strings.NewReader(tc.input)
			}
			output, err := command.CombinedOutput()
			if command.Process != nil {
				defer syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
			}
			require.NoError(t, err, "standalone CLI output: %s", strings.ReplaceAll(string(output), dsn, "[private test database]"))
			select {
			case valid := <-advertised:
				require.True(t, valid, "actual model request must advertise question schema")
			default:
				t.Fatal("model never received ask_user tool")
			}
			select {
			case result := <-toolResults:
				if !tc.terminal || tc.jsonl {
					require.Contains(t, result, "ask_user unsupported")
					require.NotContains(t, string(output), "Answer: ")
				} else {
					var answer struct {
						Answer    string `json:"answer"`
						Index     int    `json:"index"`
						Cancelled bool   `json:"cancelled"`
					}
					require.NoError(t, json.Unmarshal([]byte(result), &answer), "actual tool result %s", result)
					require.Equal(t, tc.answer, answer.Answer)
					require.Equal(t, tc.index, answer.Index)
					require.Equal(t, tc.cancelled, answer.Cancelled)
					require.Contains(t, string(output), "Fixture color?")
				}
			default:
				t.Fatal("model never received completed tool response")
			}
			require.Contains(t, string(output), "ask fixture completed")
			hookSession := ""
			for point, path := range hookCaptures {
				eventData, err := os.ReadFile(path)
				require.NoError(t, err, "actual standalone %s hook", point)
				var event struct {
					Point     string `json:"point"`
					ToolName  string `json:"tool_name"`
					SessionID string `json:"session_id"`
				}
				require.NoError(t, json.Unmarshal(eventData, &event))
				require.Equal(t, point, event.Point)
				require.Equal(t, "ask_user", event.ToolName)
				require.NotEmpty(t, event.SessionID)
				if hookSession == "" {
					hookSession = event.SessionID
				} else {
					require.Equal(t, hookSession, event.SessionID)
				}
			}
			require.ErrorIs(t, syscall.Kill(-command.Process.Pid, 0), syscall.ESRCH, fmt.Sprintf("CLI process group %d must have no remaining children", command.Process.Pid))
		})
	}
}

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
	"sync"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/acp"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/stretchr/testify/require"
)

func TestDurableACPTwoProcessesContinueIndependentWorkAndRecoverReceipt(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	type instance struct {
		home, workspace, dsn, configPath string
		session                          *session.Session
		process                          *acpCLIProcess
	}
	peers := make([]*instance, 2)
	for i := range peers {
		database, _ := testpg.Open(t)
		var schema string
		require.NoError(t, database.QueryRowContext(ctx, `SELECT current_schema()`).Scan(&schema))
		store, err := session.NewPostgresStore(database)
		require.NoError(t, err)
		item := &instance{home: t.TempDir(), workspace: t.TempDir(), dsn: acpProcessSchemaDSN(t, schema)}
		item.session = session.NewSession(item.workspace)
		item.session.Provider = "fixture"
		item.session.Model = "fixture-model"
		require.NoError(t, store.Create(ctx, item.session))
		peers[i] = item
	}
	binary := filepath.Join(t.TempDir(), "overkill")
	build := exec.CommandContext(ctx, filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", binary, ".")
	output, err := build.CombinedOutput()
	require.NoError(t, err, string(output))
	const token = "durable-acceptance-private-token"
	const providerKey = "controlled-provider-key"
	const key = "one-frontend-backend-request"
	var mu sync.Mutex
	calls := map[string]int{}
	var observedTask string
	failures := make(chan string, 16)
	failure := func(text string) {
		select {
		case failures <- text:
		default:
		}
	}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		if json.NewDecoder(r.Body).Decode(&input) != nil {
			failure("malformed provider request")
			http.Error(w, "bad fixture", 400)
			return
		}
		role := "frontend"
		if strings.HasPrefix(r.URL.Path, "/backend") {
			role = "backend"
		}
		mu.Lock()
		calls[role]++
		step := calls[role]
		mu.Unlock()
		var task acp.Task
		for _, message := range input.Messages {
			var text string
			_ = json.Unmarshal(message.Content, &text)
			if strings.Contains(text, token) {
				failure("peer credential reached model messages")
			}
			if message.Role == "tool" {
				var candidate acp.Task
				if json.Unmarshal([]byte(text), &candidate) == nil && candidate.TaskID != "" {
					task = candidate
				}
			}
		}
		tool := func(name string, args any) map[string]any {
			encoded, _ := json.Marshal(args)
			return map[string]any{"index": 0, "id": fmt.Sprintf("%s-%d", role, step), "type": "function", "function": map[string]string{"name": name, "arguments": string(encoded)}}
		}
		var delta any
		finish := "tool_calls"
		if role == "frontend" {
			switch step {
			case 1:
				delta = map[string]any{"tool_calls": []any{tool("acp_submit", map[string]any{"peer": "backend", "session": peers[1].session.ID, "content": "Build the backend endpoint", "idempotency_key": key})}}
			case 2:
				if task.TaskID == "" || task.Terminal() {
					failure("frontend did not receive a nonterminal durable handle")
				}
				mu.Lock()
				observedTask = task.TaskID
				mu.Unlock()
				delta = map[string]any{"tool_calls": []any{tool("fs", map[string]string{"action": "write", "path": "frontend-ready.txt", "content": "frontend continued while backend was busy"})}}
			case 3:
				mu.Lock()
				id := observedTask
				mu.Unlock()
				delta = map[string]any{"tool_calls": []any{tool("acp_await", map[string]any{"peer": "backend", "task_id": id, "wait_ms": 5000})}}
			case 4:
				if task.Status != "completed" || task.Reply != "Backend endpoint complete" {
					failure("frontend did not observe durable completion/reply")
				}
				finish = "stop"
				delta = map[string]string{"content": "Frontend integrated durable backend result"}
			default:
				failure("unexpected frontend provider iteration")
				finish = "stop"
				delta = map[string]string{"content": "unexpected"}
			}
		} else {
			if step == 1 {
				deadline := time.NewTimer(5 * time.Second)
				defer deadline.Stop()
				ticker := time.NewTicker(10 * time.Millisecond)
				defer ticker.Stop()
				for {
					if _, err := os.Stat(filepath.Join(peers[0].workspace, "frontend-ready.txt")); err == nil {
						break
					}
					select {
					case <-ticker.C:
					case <-deadline.C:
						failure("frontend could not continue while backend task ran")
						http.Error(w, "independent work missing", 500)
						return
					case <-r.Context().Done():
						return
					}
				}
				delta = map[string]any{"tool_calls": []any{tool("fs", map[string]string{"action": "write", "path": "endpoint.txt", "content": "backend endpoint ready"})}}
			} else {
				finish = "stop"
				delta = map[string]string{"content": "Backend endpoint complete"}
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		chunk, _ := json.Marshal(map[string]any{"id": "durable-fixture", "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}, "usage": map[string]int{"prompt_tokens": 12, "completion_tokens": 8}})
		_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
	}))
	defer provider.Close()
	configure := func(item *instance, role string, remote []config.ACPPeerConfig) {
		cfg := config.Default()
		cfg.Rewriter.Enabled = false
		cfg.Browser.Enabled = false
		cfg.Slack.Enabled = false
		cfg.Security.AutonomyLevel = "yolo"
		cfg.Agent.DefaultProvider = "fixture"
		cfg.Agent.DefaultModel = "fixture-model"
		cfg.Agent.MaxTurns = 8
		cfg.DatabaseURL = item.dsn
		cfg.ACP.Listen = "127.0.0.1:0"
		cfg.ACP.Token = token
		cfg.ACP.Peers = remote
		cfg.Providers = []config.ProviderConfig{{Name: "fixture", Type: "openai", APIKey: providerKey, BaseURL: provider.URL + "/" + role, Models: []config.ModelConfig{{ID: "fixture-model", ContextWindow: 32000, MaxOutputTokens: 1024}}}}
		item.configPath = filepath.Join(item.home, "config.toml")
		require.NoError(t, cfg.Save(item.configPath))
	}
	configure(peers[1], "backend", nil)
	peers[1].process = startACPCLIProcess(t, ctx, binary, peers[1].configPath, peers[1].home, peers[1].workspace, peers[1].dsn, []string{token, providerKey})
	peers[1].process.awaitReady(t, ctx)
	tokenFile := filepath.Join(peers[0].home, "backend.token")
	require.NoError(t, os.WriteFile(tokenFile, []byte(token), 0600))
	configure(peers[0], "frontend", []config.ACPPeerConfig{{Name: "backend", URL: "http://" + peers[1].process.address, TokenFile: tokenFile}})
	peers[0].process = startACPCLIProcess(t, ctx, binary, peers[0].configPath, peers[0].home, peers[0].workspace, peers[0].dsn, []string{token, providerKey})
	peers[0].process.awaitReady(t, ctx)
	frontend := acp.NewClient("http://"+peers[0].process.address, token)
	events, err := frontend.SendTo(ctx, peers[0].session.ID, "Build frontend and delegate backend independently")
	require.NoError(t, err)
	done := false
	var reply strings.Builder
	for event := range events {
		if event.Type == "error" {
			t.Fatalf("frontend execution: %s", event.Error)
		}
		if event.Type == "text_delta" {
			reply.WriteString(event.Content)
		}
		if event.Type == "done" {
			done = true
		}
	}
	require.True(t, done)
	require.Equal(t, "Frontend integrated durable backend result", reply.String())
	mu.Lock()
	id := observedTask
	backendCalls := calls["backend"]
	mu.Unlock()
	require.NotEmpty(t, id)
	require.Equal(t, 2, backendCalls)
	for _, entry := range []struct {
		item          *instance
		file, content string
	}{{peers[0], "frontend-ready.txt", "frontend continued while backend was busy"}, {peers[1], "endpoint.txt", "backend endpoint ready"}} {
		data, err := os.ReadFile(filepath.Join(entry.item.workspace, entry.file))
		require.NoError(t, err)
		require.Equal(t, entry.content, string(data))
	}
	backend := acp.NewClient("http://"+peers[1].process.address, token)
	task, err := backend.TaskStatus(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "completed", task.Status)
	require.NoError(t, peers[1].process.stop())
	peers[1].process = startACPCLIProcess(t, ctx, binary, peers[1].configPath, peers[1].home, peers[1].workspace, peers[1].dsn, []string{token, providerKey})
	peers[1].process.awaitReady(t, ctx)
	backend = acp.NewClient("http://"+peers[1].process.address, token)
	task, err = backend.TaskStatus(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "Backend endpoint complete", task.Reply)
	same, err := backend.SubmitTask(ctx, acp.TaskSubmitRequest{From: "overkill", Content: "Build the backend endpoint", SessionID: peers[1].session.ID, IdempotencyKey: key})
	require.NoError(t, err)
	require.Equal(t, id, same.TaskID)
	mu.Lock()
	require.Equal(t, backendCalls, calls["backend"], "receipt retry must not execute provider again")
	mu.Unlock()
	select {
	case failure := <-failures:
		t.Fatal(failure)
	default:
	}
}

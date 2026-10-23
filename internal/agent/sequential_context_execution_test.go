package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/security"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tokenizer"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
	"github.com/stretchr/testify/require"
)

const isolatedItems = "Inspect auth.ts and report its behavior\n- Inspect billing.ts and report its behavior\n- Inspect reports.ts and report its behavior"

func sequentialFiles(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for name, content := range map[string]string{"auth.ts": "AUTH_ONLY_BYTES; @unrelated.ts", "billing.ts": "BILLING_ONLY_BYTES", "reports.ts": "REPORTS_ONLY_BYTES", "unrelated.ts": "UNRELATED_SOURCE_BYTES", "credentials.ts": "AKIAIOSFODNN7EXAMPLE", ".env": "SECRET_DOT_ENV", "node_modules/auth.ts": "DEPENDENCY_FILE_BYTES"} {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0700))
		require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte(content), 0600))
	}
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "auth.ts"), []byte("ESCAPED_WORKSPACE_BYTES"), 0600))
	require.NoError(t, os.Symlink(filepath.Join(outside, "auth.ts"), filepath.Join(root, "symlink_auth.ts")))
	return root
}
func sendSequentialFixture(w http.ResponseWriter, stream bool, content string, toolCall map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	finish := "stop"
	message := map[string]any{"role": "assistant", "content": content}
	if toolCall != nil {
		finish = "tool_calls"
		message = map[string]any{"role": "assistant", "tool_calls": []any{toolCall}}
	}
	if stream {
		w.Header().Set("Content-Type", "text/event-stream")
		delta := map[string]any{"content": content}
		if toolCall != nil {
			toolCall["index"] = 0
			delta = map[string]any{"tool_calls": []any{toolCall}}
		}
		raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
		fmt.Fprintf(w, "data: %s\n\ndata: {\"choices\":[],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":2}}\n\ndata: [DONE]\n\n", raw)
	} else {
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": message, "finish_reason": finish}}, "usage": map[string]int{"prompt_tokens": 5, "completion_tokens": 2}})
	}
}
func TestSequentialRunAndStreamActualProviderFileIsolation(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream_%v", stream), func(t *testing.T) {
			root := sequentialFiles(t)
			var mu sync.Mutex
			var views []string
			var itemNums []int
			fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var wire struct {
					Stream   bool `json:"stream"`
					Messages []struct {
						Role    string `json:"role"`
						Content string `json:"content"`
					} `json:"messages"`
				}
				require.NoError(t, json.NewDecoder(r.Body).Decode(&wire))
				raw, _ := json.Marshal(wire.Messages)
				view := string(raw)
				mu.Lock()
				views = append(views, view)
				mu.Unlock()
				number := 0
				for i, name := range []string{"auth.ts", "billing.ts", "reports.ts"} {
					if strings.Contains(view, "Inspect "+name) {
						number = i + 1
						break
					}
				}
				mu.Lock()
				itemNums = append(itemNums, number)
				mu.Unlock()
				sendSequentialFixture(w, wire.Stream, fmt.Sprintf("ITEM_%d_FINISHED", number), nil)
			}))
			defer fixture.Close()
			p := providers.NewOpenAIProvider("fixture-only", nil)
			p.BaseProvider = providers.NewBaseProvider("openai", fixture.URL, "fixture-only", []providers.Model{{ID: "fixture", ContextWindow: 32768, MaxOutputTokens: 1024}})
			a := New(Config{Workspace: root, SessionID: "isolated", Provider: p, Model: "fixture", Tokenizer: tokenizer.NewEstimator(), ContextWindow: 32768, MaxTokens: 1024, MaxSteps: 3})
			defer a.Shutdown()
			a.SetHistory([]providers.Message{{Role: "user", Content: "OLD_SESSION_SOURCE_BYTES"}, {Role: "assistant", Content: "old context"}})
			a.SetContextProvider(func(context.Context, string) string { return "AMBIENT_GIT_DIFF_SOURCE_BYTES" })
			_, err := a.Run(t.Context(), "/think")
			require.NoError(t, err)
			if stream {
				events, err := a.Stream(t.Context(), isolatedItems)
				require.NoError(t, err)
				done := 0
				for event := range events {
					require.NotEqual(t, EventError, event.Type, fmt.Sprint(event.Error))
					if event.Type == EventDone {
						done++
						require.Contains(t, event.Result.Response, "3 done")
						for i := 1; i <= 3; i++ {
							require.Contains(t, event.Result.Response, fmt.Sprintf("ITEM_%d_FINISHED", i))
						}
					}
				}
				require.Equal(t, 1, done)
			} else {
				result, err := a.Run(t.Context(), isolatedItems)
				require.NoError(t, err)
				require.Contains(t, result.Response, "3 done")
				for i := 1; i <= 3; i++ {
					require.Contains(t, result.Response, fmt.Sprintf("ITEM_%d_FINISHED", i))
				}
			}
			mu.Lock()
			defer mu.Unlock()
			require.Equal(t, []int{1, 2, 3}, itemNums)
			require.Len(t, views, 3)
			for i, view := range views {
				for j, marker := range []string{"AUTH_ONLY_BYTES", "BILLING_ONLY_BYTES", "REPORTS_ONLY_BYTES"} {
					if i == j {
						require.Contains(t, view, marker)
					} else {
						require.NotContains(t, view, marker)
					}
				}
				for _, marker := range []string{"OLD_SESSION_SOURCE_BYTES", "AMBIENT_GIT_DIFF_SOURCE_BYTES", "UNRELATED_SOURCE_BYTES", "DEPENDENCY_FILE_BYTES", "ESCAPED_WORKSPACE_BYTES", "ITEM_1_FINISHED", "ITEM_2_FINISHED", "SECRET_DOT_ENV"} {
					require.NotContains(t, view, marker)
				}
			}
			raw, _ := json.Marshal(a.History())
			for _, marker := range []string{"OLD_SESSION_SOURCE_BYTES", "AUTH_ONLY_BYTES", "BILLING_ONLY_BYTES", "REPORTS_ONLY_BYTES", "ITEM_1_FINISHED", "ITEM_2_FINISHED", "ITEM_3_FINISHED"} {
				require.Contains(t, string(raw), marker)
			}
			require.False(t, a.IsBusy())
			require.Equal(t, 3, a.QueueSnapshot().Done)
		})
	}
}
func TestSequentialAutoReadsRefuseSecretsAndDependencies(t *testing.T) {
	root := sequentialFiles(t)
	workspace, err := openSequentialWorkspace(t.Context(), root)
	require.NoError(t, err)
	defer workspace.Close()
	for _, description := range []string{"Inspect credentials.ts contents", "Inspect node_modules/auth.ts contents", "Inspect .env contents", "Inspect symlink_auth.ts contents"} {
		prompt, err := workspace.ItemPrompt(t.Context(), WorkItem{Index: 1, Description: description}, 2)
		require.NoError(t, err)
		require.NotContains(t, prompt.Source, "AKIAIOSFODNN7EXAMPLE")
		require.NotContains(t, prompt.Source, "DEPENDENCY_FILE_BYTES")
		require.NotContains(t, prompt.Source, "SECRET_DOT_ENV")
		require.NotContains(t, prompt.Source, "ESCAPED_WORKSPACE_BYTES")
	}
}
func TestSequentialItemsRetainReaderAuthority(t *testing.T) {
	root := sequentialFiles(t)
	registry := tools.NewRegistry()
	require.NoError(t, registry.Register(tools.NewFSTool(root)))
	var mu sync.Mutex
	calls := map[string]int{}
	p := providers.NewMockProvider("fixture", nil, func(request providers.Request) (providers.Response, error) {
		item := ""
		for _, message := range request.Messages {
			if message.Role == "user" {
				item = message.Content
			}
		}
		mu.Lock()
		calls[item]++
		n := calls[item]
		mu.Unlock()
		if n == 1 {
			return providers.Response{ToolCalls: []providers.ToolCall{{ID: "write", Name: "fs", Arguments: `{"action":"write","path":"forbidden.txt","content":"bad"}`}}}, nil
		}
		latest := request.Messages[len(request.Messages)-1]
		if !strings.Contains(latest.Content, "reader") {
			t.Errorf("Reader denial missing: %s", latest.Content)
		}
		return providers.Response{Content: "Read-only item inspected."}, nil
	})
	a := New(Config{Workspace: root, Provider: p, Model: "fixture", Tools: registry, MaxSteps: 3})
	defer a.Shutdown()
	a.SetPrivilegeGate(security.NewPrivilegeGate(security.ModeReader))
	_, err := a.Run(t.Context(), "/think")
	require.NoError(t, err)
	result, err := a.Run(t.Context(), isolatedItems)
	require.NoError(t, err)
	require.Contains(t, result.Response, "3 done")
	_, err = os.Stat(filepath.Join(root, "forbidden.txt"))
	require.True(t, os.IsNotExist(err))
	require.Equal(t, security.ModeReader, a.PrivilegeMode())
}
func TestSequentialStreamAdmissionAndCancellation(t *testing.T) {
	root := sequentialFiles(t)
	entered := make(chan struct{})
	p := &goalTestProvider{complete: func(context.Context, providers.Request) (providers.Response, error) { return providers.Response{}, nil }, stream: func(ctx context.Context, request providers.Request) (<-chan providers.Chunk, error) {
		close(entered)
		out := make(chan providers.Chunk)
		go func() { <-ctx.Done(); close(out) }()
		return out, nil
	}}
	a := New(Config{Workspace: root, Provider: p, Model: "fixture", MaxSteps: 2})
	defer a.Shutdown()
	_, err := a.Run(t.Context(), "/think")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	events, err := a.Stream(ctx, isolatedItems)
	require.NoError(t, err)
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("item stream never began")
	}
	_, err = a.Run(t.Context(), "concurrent run")
	require.ErrorContains(t, err, "already running")
	cancel()
	for range events {
	}
	require.Eventually(t, func() bool { return !a.IsBusy() }, time.Second, time.Millisecond)
	state := a.QueueSnapshot()
	require.False(t, state.Active)
	require.Equal(t, "failed", state.Items[0].Status)
	require.Equal(t, "skipped", state.Items[1].Status)
	require.Equal(t, "skipped", state.Items[2].Status)
}

func TestSequentialStreamOneGoalLeaseAndActualUsage(t *testing.T) {
	gs := newExecutionGoalStore(t)
	p := &goalTestProvider{stream: func(ctx context.Context, _ providers.Request) (<-chan providers.Chunk, error) {
		out := make(chan providers.Chunk, 1)
		out <- providers.Chunk{Content: "done", Done: true, Usage: &providers.Usage{InputTokens: 5, OutputTokens: 2}}
		close(out)
		return out, nil
	}}
	a := goalAgent(p, gs, "sequential-stream-goal")
	defer a.Shutdown()
	_, err := a.Run(t.Context(), "/think")
	require.NoError(t, err)
	require.NoError(t, gs.SetWithBudgets(t.Context(), a.SessionID(), "finish all three items", 1000, 0))
	events, err := a.Stream(t.Context(), sequentialFixture)
	require.NoError(t, err)
	done := 0
	for event := range events {
		require.NotEqual(t, EventError, event.Type, fmt.Sprint(event.Error))
		if event.Type == EventDone {
			done++
		}
	}
	require.Equal(t, 1, done)
	g := requireGoal(t, gs, a.SessionID())
	require.Equal(t, 21, g.TokensUsed)
	var rows, tokens int
	require.NoError(t, gs.db.QueryRow(`SELECT count(*),sum(tokens_used) FROM goal_turn_usage WHERE session_id=$1`, a.SessionID()).Scan(&rows, &tokens))
	require.Equal(t, 1, rows)
	require.Equal(t, 21, tokens)
}

func TestSequentialExplicitPathWinsOverSubstringAndKeywords(t *testing.T) {
	root := t.TempDir()
	for name, content := range map[string]string{"auth.ts": "WRONG_AUTH_BYTES", "oauth.ts": "RIGHT_OAUTH_BYTES", "behavior.ts": "WRONG_GENERIC_BYTES", "nested/oauth.ts": "WRONG_NESTED_BYTES"} {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0700))
		require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte(content), 0600))
	}
	workspace, err := openSequentialWorkspace(t.Context(), root)
	require.NoError(t, err)
	defer workspace.Close()
	prompt, err := workspace.ItemPrompt(t.Context(), WorkItem{Index: 1, Description: "Inspect oauth.ts and report its behavior"}, 2)
	require.NoError(t, err)
	require.Contains(t, prompt.Source, "RIGHT_OAUTH_BYTES")
	for _, wrong := range []string{"WRONG_AUTH_BYTES", "WRONG_GENERIC_BYTES", "WRONG_NESTED_BYTES"} {
		require.NotContains(t, prompt.Source, wrong)
	}
}

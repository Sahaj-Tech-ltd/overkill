package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/api"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/stretchr/testify/require"
)

func TestSharedRuntimeDAGActualRunStreamMemoryToolsAndReload(t *testing.T) {
	db, _ := testpg.Open(t)
	sessions, err := session.NewPostgresStore(db)
	require.NoError(t, err)
	var mu sync.Mutex
	steps := map[string]int{}
	summaryCalls, mainCalls := 0, 0
	seenTools := map[string]bool{}
	metadata := map[string]*session.Session{}
	var services *runtimeServices
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Model     string          `json:"model"`
			Stream    bool            `json:"stream"`
			MaxTokens int             `json:"max_tokens"`
			Tools     json.RawMessage `json:"tools"`
			Messages  []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&request); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if request.Model == "summary" {
			mu.Lock()
			summaryCalls++
			mu.Unlock()
			if request.Stream || len(request.Tools) > 0 && string(request.Tools) != "null" {
				t.Error("summary attempted tools or streaming")
			}
			if request.MaxTokens > 128 {
				t.Errorf("summary config limit ignored: %d", request.MaxTokens)
			}
			json.NewEncoder(w).Encode(map[string]any{"model": "summary", "choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "Prior user constraints and exact source evidence remain in durable memory."}, "finish_reason": "stop"}}, "usage": map[string]int{"prompt_tokens": 80, "completion_tokens": 14}})
			return
		}
		mu.Lock()
		steps[request.Model]++
		step := steps[request.Model]
		mainCalls++
		mu.Unlock()
		if !strings.Contains(string(request.Tools), "memory_search") || !strings.Contains(string(request.Tools), "memory_expand") {
			t.Error("model did not receive durable memory tools")
		}
		if len(request.Messages) < 2 || !strings.Contains(request.Messages[1].Content, "[seahorse summary ") {
			all, _ := json.Marshal(request.Messages)
			if !strings.Contains(string(all), "[seahorse summary ") {
				t.Error("main model called before durable compaction")
			}
		}
		var toolName string
		var arguments any
		switch step {
		case 1:
			toolName = "memory_search"
			arguments = map[string]any{"query": "wombat", "limit": 5}
		case 2:
			latest := request.Messages[len(request.Messages)-1]
			if latest.Role != "tool" || !strings.Contains(latest.Content, "wombat") {
				t.Error("actual search tool result omitted raw source evidence")
			}
			toolName = "memory_describe"
			arguments = map[string]any{}
		case 3:
			latest := request.Messages[len(request.Messages)-1]
			if latest.Role != "tool" || !strings.Contains(latest.Content, "parentIds") && !strings.Contains(latest.Content, "source_ids") {
				t.Error("actual describe tool omitted persisted lineage")
			}
			hits, err := services.Compaction.Store.SearchSources(r.Context(), metadata[request.Model].ID, "wombat", 5)
			if err != nil || len(hits) != 1 {
				t.Errorf("fixture cannot resolve bound raw source: %v %#v", err, hits)
				w.WriteHeader(500)
				return
			}
			toolName = "memory_expand"
			arguments = map[string]any{"source_id": hits[0].SourceID}
		case 4:
			latest := request.Messages[len(request.Messages)-1]
			if latest.Role != "tool" || !strings.Contains(latest.Content, "wombat original contract") {
				t.Error("actual expansion tool did not recover original source")
			}
		}
		var message map[string]any
		finish := "stop"
		if toolName != "" {
			mu.Lock()
			seenTools[request.Model+":"+toolName] = true
			mu.Unlock()
			raw, _ := json.Marshal(arguments)
			message = map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"id": fmt.Sprintf("%s-%d", request.Model, step), "type": "function", "function": map[string]any{"name": toolName, "arguments": string(raw)}}}}
			finish = "tool_calls"
		} else {
			message = map[string]any{"role": "assistant", "content": "Recovered original conversation evidence."}
		}
		if request.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			delta := map[string]any{"content": message["content"]}
			if toolName != "" {
				calls := message["tool_calls"].([]any)
				call := calls[0].(map[string]any)
				call["index"] = 0
				delta = map[string]any{"tool_calls": calls}
			}
			raw, _ := json.Marshal(map[string]any{"model": request.Model, "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
			fmt.Fprintf(w, "data: %s\n\n", raw)
			fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":100,\"completion_tokens\":20}}\n\ndata: [DONE]\n\n")
		} else {
			json.NewEncoder(w).Encode(map[string]any{"model": request.Model, "choices": []any{map[string]any{"message": message, "finish_reason": finish}}, "usage": map[string]int{"prompt_tokens": 100, "completion_tokens": 20}})
		}
	}))
	defer fixture.Close()
	cfg := config.Default()
	cfg.Agent.DefaultProvider = "fixture"
	cfg.Agent.DefaultModel = "run-main"
	cfg.Agent.MaxTurns = 8
	cfg.Agent.MaxOutputTokens = 1024
	cfg.Compaction.Strategy = "auto"
	cfg.Compaction.Model = "summary"
	cfg.Compaction.PreserveMessages = 2
	cfg.Compaction.MaxSummaryTokens = 128
	cfg.Providers = []config.ProviderConfig{{Name: "fixture", Type: "openai", APIKey: "local-fixture-only", BaseURL: fixture.URL, Models: []config.ModelConfig{{ID: "run-main", ContextWindow: 65536, MaxOutputTokens: 1024}, {ID: "stream-main", ContextWindow: 65536, MaxOutputTokens: 1024}, {ID: "summary", ContextWindow: 16384, MaxOutputTokens: 256}}}}
	home := t.TempDir()
	services, err = newRuntimeServices(cfg, db, home)
	require.NoError(t, err)
	require.Equal(t, "seahorse", services.Compaction.Strategy())
	for _, model := range []string{"run-main", "stream-main"} {
		sess := session.NewSession(t.TempDir())
		sess.Model = model
		sess.Provider = "fixture"
		for i := 0; i < 80; i++ {
			role := "user"
			if i%2 == 1 {
				role = "assistant"
			}
			content := fmt.Sprintf("historical %d: %s", i, strings.Repeat("observed evidence and exact file state; ", 110))
			if i == 0 {
				content = "wombat original contract: preserve this exact user instruction. " + content
			}
			sess.Messages = append(sess.Messages, providers.Message{Role: role, Content: content})
		}
		require.NoError(t, sessions.Create(t.Context(), sess))
		metadata[model] = sess
	}
	server := api.NewServer(services.APIConfig(sessions))
	runCtx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- server.Start(runCtx) }()
	defer func() { server.Close(); cancel(); require.NoError(t, <-done) }()
	require.Eventually(t, func() bool { return server.Addr() != "" && !strings.HasSuffix(server.Addr(), ":0") }, 3*time.Second, 10*time.Millisecond)
	request := func(method, path, body string) string {
		ctx, stop := context.WithTimeout(t.Context(), 30*time.Second)
		defer stop()
		req, err := http.NewRequestWithContext(ctx, method, server.Addr()+path, strings.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+server.AuthToken())
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		raw, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Equal(t, 200, resp.StatusCode, string(raw))
		return string(raw)
	}
	raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "agent.send", "params": map[string]string{"session_id": metadata["run-main"].ID, "message": "Recover the exact original contract from memory."}})
	response := request("POST", "/rpc", string(raw))
	require.NotContains(t, response, `"error":`)
	require.Contains(t, response, "Recovered original conversation evidence")
	response = request("GET", "/stream?session_id="+url.QueryEscape(metadata["stream-main"].ID)+"&message="+url.QueryEscape("Recover the exact original contract from memory."), "")
	require.NotContains(t, response, "event: error")
	require.Contains(t, response, "Recovered original conversation evidence")
	mu.Lock()
	require.Greater(t, summaryCalls, 2)
	require.Equal(t, 8, mainCalls)
	for _, model := range []string{"run-main", "stream-main"} {
		for _, name := range []string{"memory_search", "memory_describe", "memory_expand"} {
			require.True(t, seenTools[model+":"+name])
		}
	}
	mu.Unlock()
	for _, model := range []string{"run-main", "stream-main"} {
		sess := metadata[model]
		state, err := services.Compaction.Store.Load(t.Context(), sess.ID)
		require.NoError(t, err)
		require.NotEmpty(t, state.HeadID)
		require.EqualValues(t, 1, state.Revision)
		persisted, err := sessions.Load(t.Context(), sess.ID)
		require.NoError(t, err)
		require.Contains(t, persisted.Messages[0].Content, "[seahorse summary ")
		require.Less(t, len(persisted.Messages), 20)
		// A fresh runtime service object can read the same durable node/source data.
		reloaded, err := newRuntimeServices(cfg, db, home)
		require.NoError(t, err)
		node, err := reloaded.Compaction.Store.Node(t.Context(), sess.ID, state.HeadID)
		require.NoError(t, err)
		require.Greater(t, node.Summary.Depth, 0)
		hits, err := reloaded.Compaction.Store.SearchSources(t.Context(), sess.ID, "wombat", 5)
		require.NoError(t, err)
		require.Len(t, hits, 1)
	}
}
func TestRuntimeCompactionExplicitChoiceAndUnavailableDAG(t *testing.T) {
	cfg := config.Default()
	cfg.Compaction.Strategy = "seahorse"
	_, err := newRuntimeServices(cfg, nil, t.TempDir())
	require.ErrorContains(t, err, "requires durable PostgreSQL")
	cfg.Compaction.Strategy = "lcm"
	services, err := newRuntimeServices(cfg, nil, t.TempDir())
	require.NoError(t, err)
	require.Equal(t, "lcm", services.Compaction.Strategy())
	cfg.Compaction.UseLCM = false
	cfg.Compaction.Strategy = "seahorse"
	services, err = newRuntimeServices(cfg, nil, t.TempDir())
	require.NoError(t, err)
	require.Equal(t, "legacy", services.Compaction.Strategy())
}

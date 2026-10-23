package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
	"github.com/stretchr/testify/require"
)

func TestRealHTTPFlowResumesAfterBackendRestartWithToolsAndNoInputReplay(t *testing.T) {
	database, _ := testpg.Open(t)
	sessions, err := session.NewPostgresStore(database)
	require.NoError(t, err)
	queue, err := agent.NewFlowQueue(database, time.Hour)
	require.NoError(t, err)
	workspace, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	metadata := session.NewSession(workspace)
	metadata.Model = "gpt-4o-mini"
	metadata.Provider = "fixture"
	require.NoError(t, sessions.Create(t.Context(), metadata))
	var calls, firstWrites, secondWrites atomic.Int32
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []struct {
				Role       string `json:"role"`
				Content    string `json:"content"`
				ToolCallID string `json:"tool_call_id"`
			} `json:"messages"`
			Stream bool `json:"stream"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		require.True(t, request.Stream)
		users := 0
		first, second := false, false
		for _, message := range request.Messages {
			if message.Role == "user" {
				users++
			}
			if message.Role == "tool" && message.ToolCallID == "first-write" {
				first = true
			}
			if message.Role == "tool" && message.ToolCallID == "second-write" {
				second = true
			}
		}
		require.Equal(t, 1, users, "continuation must not append duplicate original input")
		calls.Add(1)
		delta := map[string]any{}
		finish := "stop"
		if !first || !second {
			id, path := "first-write", "first.txt"
			if first {
				id, path = "second-write", "second.txt"
				secondWrites.Add(1)
			} else {
				firstWrites.Add(1)
			}
			arguments, _ := json.Marshal(map[string]string{"action": "write", "path": path, "content": id})
			delta["tool_calls"] = []any{map[string]any{"index": 0, "id": id, "type": "function", "function": map[string]string{"name": "fs", "arguments": string(arguments)}}}
			finish = "tool_calls"
		} else {
			delta["content"] = "Durable task completed after restart."
		}
		w.Header().Set("Content-Type", "text/event-stream")
		data, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
		fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", data)
	}))
	defer fixture.Close()
	cfg := config.Default()
	cfg.Agent.DefaultProvider = "fixture"
	cfg.Agent.DefaultModel = "gpt-4o-mini"
	cfg.Agent.MaxTurns = 1
	cfg.Providers = []config.ProviderConfig{{Name: "fixture", Type: "openai", APIKey: "fake-only", BaseURL: fixture.URL}}
	sc := ServerConfig{Config: cfg, SessionStore: sessions, FlowQueue: queue, SessionRuntimeFactory: func(_ context.Context, metadata *session.Session) (*SessionRuntime, error) {
		registry := tools.NewRegistry()
		require.NoError(t, registry.Register(tools.NewFSTool(metadata.Folder)))
		return &SessionRuntime{Tools: registry, FlowStore: queue.Bind(metadata.ID, metadata.Folder, metadata.Model, metadata.Provider)}, nil
	}}
	start := func() (*Server, context.CancelFunc, <-chan error) {
		server := NewServer(sc)
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- server.Start(ctx) }()
		select {
		case <-server.Ready():
		case <-time.After(5 * time.Second):
			t.Fatal("HTTP backend did not start")
		}
		return server, cancel, done
	}
	first, cancel, done := start()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, first.Addr()+"/stream?session_id="+url.QueryEscape(metadata.ID)+"&message="+url.QueryEscape("Complete both workspace files."), nil)
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer "+first.AuthToken())
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	require.NoError(t, err)
	require.Equal(t, 200, response.StatusCode)
	require.Contains(t, string(body), "flow_checkpoint")
	require.EqualValues(t, 1, calls.Load())
	cancel()
	require.NoError(t, <-done)
	jobs, err := queue.ListSession(t.Context(), metadata.ID)
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	require.Equal(t, "queued", jobs[0].Status)
	persisted, err := sessions.Load(t.Context(), metadata.ID)
	require.NoError(t, err)
	require.Len(t, persisted.Messages, 3, "checkpoint and transcript must commit together before restart")
	_, err = database.ExecContext(t.Context(), `UPDATE flow_resume_jobs SET due_at=NOW() WHERE session_id=$1`, metadata.ID)
	require.NoError(t, err)
	queue, err = agent.NewFlowQueue(database, time.Millisecond)
	require.NoError(t, err)
	sc.FlowQueue = queue
	second, cancelSecond, doneSecond := start()
	defer func() { cancelSecond(); require.NoError(t, <-doneSecond) }()
	_ = second
	require.Eventually(t, func() bool {
		jobs, err := queue.ListSession(t.Context(), metadata.ID)
		return err == nil && len(jobs) == 1 && jobs[0].Status == "completed"
	}, 8*time.Second, 30*time.Millisecond)
	require.EqualValues(t, 3, calls.Load())
	require.EqualValues(t, 1, firstWrites.Load())
	require.EqualValues(t, 1, secondWrites.Load())
	for _, path := range []string{"first.txt", "second.txt"} {
		raw, err := os.ReadFile(filepath.Join(workspace, path))
		require.NoError(t, err)
		require.NotEmpty(t, raw)
	}
	persisted, err = sessions.Load(t.Context(), metadata.ID)
	require.NoError(t, err)
	users := 0
	for _, message := range persisted.Messages {
		if message.Role == "user" {
			users++
		}
	}
	require.Equal(t, 1, users)
	require.Equal(t, "Durable task completed after restart.", persisted.Messages[len(persisted.Messages)-1].Content)
	t.Logf("real HTTP + PostgreSQL restart receipt: calls=%d first_write=%d second_write=%d history_messages=%d", calls.Load(), firstWrites.Load(), secondWrites.Load(), len(persisted.Messages))
}

func TestPendingFlowCannotRunAfterConversationWorkspaceModelOrStatusChanges(t *testing.T) {
	for _, change := range []string{"conversation", "workspace", "model", "provider", "inactive"} {
		t.Run(change, func(t *testing.T) {
			database, _ := testpg.Open(t)
			sessions, err := session.NewPostgresStore(database)
			require.NoError(t, err)
			queue, err := agent.NewFlowQueue(database, time.Millisecond)
			require.NoError(t, err)
			workspace, err := filepath.EvalSymlinks(t.TempDir())
			require.NoError(t, err)
			metadata := session.NewSession(workspace)
			metadata.Model = "fixture"
			metadata.Provider = "fixture"
			require.NoError(t, sessions.Create(t.Context(), metadata))
			bound := queue.Bind(metadata.ID, workspace, "fixture", "fixture")
			require.NoError(t, bound.BeginTurn(t.Context()))
			state := &agent.FlowState{ID: "stale", SessionID: metadata.ID, Model: "fixture", UserInput: "old task", History: []providers.Message{{Role: "user", Content: "old task"}}, Reason: "deadline_exceeded"}
			require.NoError(t, bound.Save(state))
			metadata, err = sessions.Load(t.Context(), metadata.ID)
			require.NoError(t, err)
			switch change {
			case "conversation":
				metadata.Messages = append(metadata.Messages, providers.Message{Role: "user", Content: "new human turn"})
			case "workspace":
				metadata.Folder = t.TempDir()
			case "model":
				metadata.Model = "other-model"
			case "provider":
				metadata.Provider = "other-provider"
			case "inactive":
				metadata.Status = "archived"
			}
			require.NoError(t, sessions.Save(t.Context(), metadata))
			time.Sleep(3 * time.Millisecond)
			cfg := config.Default()
			cfg.Agent.DefaultProvider = "fixture"
			cfg.Agent.DefaultModel = "fixture"
			server := NewServer(ServerConfig{Config: cfg, SessionStore: sessions, FlowQueue: queue})
			defer server.Close()
			server.resumeQueuedFlow(t.Context(), state.ID)
			jobs, err := queue.ListSession(t.Context(), metadata.ID)
			require.NoError(t, err)
			require.Len(t, jobs, 1)
			require.Equal(t, "cancelled", jobs[0].Status)
			require.Empty(t, server.agents, "stale continuation must not create an executing agent")
		})
	}
}

func TestAbortAndEstopStillCancelActualProviderWhenDatabaseIsUnavailable(t *testing.T) {
	for _, estop := range []bool{false, true} {
		t.Run(fmt.Sprint(estop), func(t *testing.T) {
			database, _ := testpg.Open(t)
			sessions, err := session.NewPostgresStore(database)
			require.NoError(t, err)
			queue, err := agent.NewFlowQueue(database, time.Millisecond)
			require.NoError(t, err)
			metadata := session.NewSession(t.TempDir())
			require.NoError(t, sessions.Create(t.Context(), metadata))
			provider := &abortTurnProvider{entered: make(chan struct{})}
			a := agent.New(agent.Config{Provider: provider, SessionID: metadata.ID, Model: "fixture", MaxSteps: 2})
			defer a.Shutdown()
			server := NewServer(ServerConfig{Config: config.Default(), SessionStore: sessions, FlowQueue: queue})
			defer server.Close()
			server.agents[metadata.ID] = a
			ctx, finish := server.bindSessionCancellation(t.Context(), metadata.ID)
			defer finish()
			done := make(chan error, 1)
			go func() { _, err := a.Run(ctx, "wait"); done <- err }()
			select {
			case <-provider.entered:
			case <-time.After(3 * time.Second):
				t.Fatal("provider did not start")
			}
			require.NoError(t, database.Close())
			raw, _ := json.Marshal(map[string]string{"session_id": metadata.ID})
			var rpcErr *RPCError
			if estop {
				_, rpcErr = server.handleEStop(t.Context(), nil)
			} else {
				_, rpcErr = server.handleAgentAbort(t.Context(), raw)
			}
			require.NotNil(t, rpcErr, "durable pause failure must be reported honestly")
			select {
			case err := <-done:
				require.Error(t, err)
			case <-time.After(time.Second):
				t.Fatal("database failure prevented immediate provider cancellation")
			}
			if estop {
				due, err := queue.Due(t.Context())
				require.NoError(t, err)
				require.Empty(t, due, "local queue must remain halted even when durable confirmation failed")
			}
		})
	}
}

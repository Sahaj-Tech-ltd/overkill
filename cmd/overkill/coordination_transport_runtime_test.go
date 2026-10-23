package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/acp"
	"github.com/Sahaj-Tech-ltd/overkill/internal/api"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/coordination"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/stretchr/testify/require"
)

func TestSameHubACPBoundModelRuntimeHandoffAtSafeBoundary(t *testing.T) {
	database, _ := testpg.Open(t)
	sessions, err := session.NewPostgresStore(database)
	require.NoError(t, err)
	var frontSteps, backSteps, remoteSends, remoteReplies, builds, cleanups atomic.Int32
	frontHeld := make(chan struct{})
	releaseFront := make(chan struct{})
	defer func() {
		select {
		case <-releaseFront:
		default:
			close(releaseFront)
		}
	}()
	var rootID string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
			Tools json.RawMessage `json:"tools"`
		}
		if json.NewDecoder(r.Body).Decode(&input) != nil {
			t.Error("invalid provider body")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		stop := func(text string) {
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": text}, "finish_reason": "stop"}}})
		}
		tool := func(name string, args any) {
			raw, _ := json.Marshal(args)
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"id": name + "-call", "type": "function", "function": map[string]any{"name": name, "arguments": string(raw)}}}}, "finish_reason": "tool_calls"}}})
		}
		if input.Model == "frontend" {
			latest := input.Messages[len(input.Messages)-1]
			if latest.Role == "user" && latest.Content == "warm" {
				stop("warm complete")
				return
			}
			if !strings.Contains(string(input.Tools), "Scoped ACP transport") {
				t.Error("model catalogue did not receive ACP-bound tools")
			}
			switch frontSteps.Add(1) {
			case 1:
				tool("agent_send", map[string]any{"to": "backend", "kind": "request", "task_id": "users-task", "payload": map[string]string{"question": "Is GET /users ready; what is the response schema?"}, "idempotency_key": "runtime-users"})
			case 2:
				tool("fs", map[string]string{"action": "write", "path": "ui.txt", "content": "Independent UI work while backend runs"})
			case 3:
				close(frontHeld)
				select {
				case <-releaseFront:
					stop("Independent UI work saved")
				case <-r.Context().Done():
					return
				}
			default:
				encoded, _ := json.Marshal(input.Messages)
				if !strings.Contains(string(encoded), "users[]") {
					t.Error("safe turn omitted correlated backend schema")
				}
				stop("Frontend received users[] at safe boundary")
			}
		} else {
			switch backSteps.Add(1) {
			case 1:
				select {
				case <-frontHeld:
				case <-r.Context().Done():
					return
				}
				if err := database.QueryRowContext(r.Context(), `SELECT id FROM coordination_messages WHERE kind='request' AND task_id='users-task'`).Scan(&rootID); err != nil {
					t.Error(err)
					return
				}
				if !strings.Contains(string(input.Tools), "Scoped ACP transport") {
					t.Error("backend catalogue missing ACP-bound reply")
				}
				tool("agent_reply", map[string]any{"message_id": rootID, "payload": map[string]string{"schema": "users[]", "endpoint": "GET /users"}, "idempotency_key": "runtime-answer"})
			default:
				stop("Backend sent schema")
			}
		}
	}))
	defer provider.Close()
	cfg := config.Default()
	cfg.Agent.DefaultProvider = "fixture"
	cfg.Agent.DefaultModel = "frontend"
	cfg.Providers = []config.ProviderConfig{{Name: "fixture", Type: "openai", BaseURL: provider.URL, APIKey: "fixture-only"}}
	services, err := newRuntimeServices(cfg, database, t.TempDir())
	require.NoError(t, err)
	var metadata []*session.Session
	var peers []coordination.Peer
	var files []string
	for _, name := range []string{"frontend", "backend"} {
		s := session.NewSession(t.TempDir())
		s.Model = name
		s.Provider = "fixture"
		require.NoError(t, sessions.Create(t.Context(), s))
		peer, token, err := services.Coordination.RegisterPeer(t.Context(), coordination.Peer{ProjectID: "same-hub", Name: name, Role: name, Workspace: s.Folder})
		require.NoError(t, err)
		require.NoError(t, services.Coordination.BindSession(t.Context(), peer.ID, s.ID))
		path := filepath.Join(t.TempDir(), "scoped-key")
		require.NoError(t, os.WriteFile(path, []byte(token), 0600))
		metadata = append(metadata, s)
		peers = append(peers, peer)
		files = append(files, path)
	}
	hub := acp.NewServer(acp.Config{Token: "global-not-peer", Coordination: services.Coordination})
	defer hub.Shutdown(context.Background())
	handler := hub.Handler()
	ingress := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/send") {
			remoteSends.Add(1)
		}
		if strings.HasSuffix(r.URL.Path, "/reply") {
			remoteReplies.Add(1)
		}
		handler.ServeHTTP(w, r)
	}))
	defer ingress.Close()
	serverCfg := services.APIConfig(sessions)
	factory := serverCfg.SessionRuntimeFactory
	serverCfg.SessionRuntimeFactory = func(ctx context.Context, s *session.Session) (*api.SessionRuntime, error) {
		runtime, err := factory(ctx, s)
		if err == nil {
			builds.Add(1)
			prior := runtime.Cleanup
			runtime.Cleanup = func() {
				cleanups.Add(1)
				if prior != nil {
					prior()
				}
			}
		}
		return runtime, err
	}
	server := api.NewServer(serverCfg)
	runCtx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- server.Start(runCtx) }()
	defer func() { server.Close(); cancel(); require.NoError(t, <-done) }()
	require.Eventually(t, func() bool { return server.Addr() != "" && !strings.HasSuffix(server.Addr(), ":0") }, 3*time.Second, 10*time.Millisecond)
	rpc := func(method string, params any) (json.RawMessage, bool) {
		raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
		ctx, stop := context.WithTimeout(t.Context(), 20*time.Second)
		defer stop()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, server.Addr()+"/rpc", strings.NewReader(string(raw)))
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+server.AuthToken())
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		data, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		var envelope struct {
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		require.NoError(t, json.Unmarshal(data, &envelope))
		return envelope.Result, len(envelope.Error) > 0 && string(envelope.Error) != "null"
	}
	_, failed := rpc("agent.send", map[string]any{"session_id": metadata[0].ID, "message": "warm"})
	require.False(t, failed)
	require.Equal(t, int32(1), builds.Load())
	_, failed = rpc("coordination.transport.bind", map[string]any{"session_id": metadata[0].ID, "endpoint": ingress.URL, "credential_file": files[1]})
	require.True(t, failed)
	appKey := filepath.Join(t.TempDir(), "app-private-key")
	require.NoError(t, os.WriteFile(appKey, []byte(server.AuthToken()), 0600))
	runBindingCLI := func(operation string, index int) {
		command := newAgentPeersCommand()
		var output bytes.Buffer
		command.SetOut(&output)
		command.SetErr(&output)
		args := []string{operation, "--session", metadata[index].ID, "--app-endpoint", server.Addr(), "--app-token-file", appKey}
		if operation == "bind-acp" {
			args = append(args, "--endpoint", ingress.URL, "--credential-file", files[index])
		}
		command.SetArgs(args)
		require.NoError(t, command.ExecuteContext(t.Context()))
		require.NotContains(t, output.String(), server.AuthToken())
		require.NotContains(t, output.String(), "credential_file")
	}
	for i := range metadata {
		if i == 0 {
			_, failed = rpc("coordination.transport.bind", map[string]any{"session_id": metadata[i].ID, "endpoint": ingress.URL, "credential_file": files[i]})
			require.False(t, failed)
		} else {
			runBindingCLI("bind-acp", i)
		}
	}
	require.Equal(t, int32(1), cleanups.Load(), "binding must discard the already loaded idle runtime")
	result := make(chan bool, 1)
	go func() {
		_, failed := rpc("agent.send", map[string]any{"session_id": metadata[0].ID, "message": "Build the UI and ask backend for GET /users"})
		result <- failed
	}()
	select {
	case <-frontHeld:
	case <-time.After(10 * time.Second):
		t.Fatal("frontend did not continue independent work")
	}
	artifact, err := os.ReadFile(filepath.Join(metadata[0].Folder, "ui.txt"))
	require.NoError(t, err)
	require.Contains(t, string(artifact), "Independent UI work")
	var answer coordination.Message
	require.Eventually(t, func() bool {
		inbox, err := services.Coordination.Inbox(t.Context(), peers[0].ID, 20)
		if err != nil {
			return false
		}
		for _, m := range inbox {
			if m.Kind == "answer" {
				answer = m
				return true
			}
		}
		return false
	}, 10*time.Second, 20*time.Millisecond)
	require.Equal(t, coordination.Accepted, answer.State)
	require.Equal(t, int32(3), frontSteps.Load(), "answer cannot enter an active frontend turn")
	_, failed = rpc("coordination.transport.bind", map[string]any{"session_id": metadata[0].ID, "endpoint": ingress.URL, "credential_file": files[0]})
	require.True(t, failed, "binding must not replace a running turn's catalogue")
	close(releaseFront)
	require.False(t, <-result)
	require.Eventually(t, func() bool {
		m, err := services.Coordination.Read(t.Context(), peers[0].ID, answer.ID)
		return err == nil && m.State == coordination.Completed
	}, 10*time.Second, 20*time.Millisecond)
	request, err := services.Coordination.Read(t.Context(), peers[0].ID, answer.InReplyTo)
	require.NoError(t, err)
	require.Equal(t, request.ConversationID, answer.ConversationID)
	require.Equal(t, "users-task", answer.TaskID)
	require.Equal(t, coordination.Completed, request.State)
	loaded, err := sessions.Load(t.Context(), metadata[0].ID)
	require.NoError(t, err)
	transcript, _ := json.Marshal(loaded.Messages)
	require.Contains(t, string(transcript), "Frontend received users[] at safe boundary")
	require.Equal(t, int32(1), remoteSends.Load())
	require.Equal(t, int32(1), remoteReplies.Load())
	// Fresh assembly reloads the durable binding; no model/global config selects it.
	restored, err := services.BuildSession(t.Context(), metadata[0])
	require.NoError(t, err)
	defer restored.Cleanup()
	tool, ok := restored.Tools.Get("agent_send")
	require.NoError(t, ok)
	description := tool.(interface{ Description() string }).Description()
	require.Contains(t, description, "Scoped ACP transport")
	require.Eventually(t, func() bool {
		var count int
		err := database.QueryRowContext(t.Context(), `SELECT count(*) FROM coordination_leases`).Scan(&count)
		return err == nil && count == 0
	}, 3*time.Second, 10*time.Millisecond)
	runBindingCLI("clear-acp", 0)
	binding, err := services.Coordination.SessionTransport(t.Context(), metadata[0].ID)
	require.NoError(t, err)
	require.Zero(t, binding.Revision)
	current, err := restored.CheckCurrent(t.Context())
	require.NoError(t, err)
	require.False(t, current, "loaded runtime must detect external cached binding revision changes")
	_, failed = rpc("agent.send", map[string]any{"session_id": metadata[0].ID, "message": "warm"})
	require.False(t, failed)

}

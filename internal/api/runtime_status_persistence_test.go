package api

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
	"github.com/stretchr/testify/require"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeStatusDurableRunStreamConfidenceAndSessionIsolation(t *testing.T) {
	db, _ := testpg.Open(t)
	store, err := session.NewPostgresStore(db)
	require.NoError(t, err)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Stream   bool                             `json:"stream"`
			Messages []struct{ Role, Content string } `json:"messages"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		finished := false
		for _, m := range request.Messages {
			if m.Role == "tool" {
				finished = true
			}
		}
		path := "run.json"
		for _, m := range request.Messages {
			if m.Role == "user" && strings.Contains(m.Content, "stream") {
				path = "stream.json"
			}
		}
		var message map[string]any
		reason := "stop"
		if finished {
			message = map[string]any{"role": "assistant", "content": "A model claim of passing is not a checker."}
		} else {
			args, _ := json.Marshal(map[string]string{"action": "write", "path": path, "content": `{"actual":true}`})
			message = map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"index": 0, "id": "write-check", "type": "function", "function": map[string]string{"name": "fs", "arguments": string(args)}}}}
			reason = "tool_calls"
		}
		if request.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			delta := map[string]any{}
			for k, v := range message {
				if k != "role" {
					delta[k] = v
				}
			}
			chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta, "finish_reason": reason}}})
			fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": message, "finish_reason": reason}}})
	}))
	defer provider.Close()
	cfg := config.Default()
	cfg.Agent.DefaultProvider, cfg.Agent.DefaultModel = "fixture", "gpt-4o-mini"
	cfg.Providers = []config.ProviderConfig{{Name: "fixture", Type: "openai", BaseURL: provider.URL, APIKey: "test-only"}}
	factory := func(_ context.Context, m *session.Session) (*SessionRuntime, error) {
		registry := tools.NewRegistry()
		require.NoError(t, registry.Register(tools.NewFSTool(m.Folder)))
		return &SessionRuntime{Tools: registry, PostWriteVerifier: &statusJSONVerifier{root: m.Folder}}, nil
	}
	server := NewServer(ServerConfig{Config: cfg, SessionStore: store, SessionRuntimeFactory: factory})
	defer server.closeSessionRuntimes()
	mux := http.NewServeMux()
	mux.HandleFunc("/rpc", server.handleRPC)
	mux.HandleFunc("/stream", server.handleStream)
	httpServer := httptest.NewServer(mux)
	defer httpServer.Close()
	ids := make([]string, 3)
	for i := range ids {
		metadata := session.NewSession(t.TempDir())
		ids[i] = metadata.ID
		require.NoError(t, store.Create(t.Context(), metadata))
	}
	params, _ := json.Marshal(map[string]string{"session_id": ids[0], "message": "write run fixture"})
	_, rpcErr := server.handleAgentSend(t.Context(), params)
	require.Nil(t, rpcErr)
	response, err := http.Get(httpServer.URL + "/stream?session_id=" + url.QueryEscape(ids[1]) + "&message=" + url.QueryEscape("write stream fixture"))
	require.NoError(t, err)
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	require.NoError(t, err)
	require.Equal(t, 200, response.StatusCode)
	require.Contains(t, string(body), "event: done")
	require.NotContains(t, string(body), "event: error")
	for i, id := range ids[:2] {
		params, _ := json.Marshal(map[string]string{"session_id": id})
		raw, rpcErr := server.testResults(t.Context(), params)
		require.Nil(t, rpcErr)
		encoded, _ := json.Marshal(raw)
		var tests map[string]any
		require.NoError(t, json.Unmarshal(encoded, &tests))
		require.EqualValues(t, 1, tests["passed"])
		require.Equal(t, "live", tests["snapshot_origin"])
		require.Equal(t, true, tests["persistence_available"])
		require.Equal(t, false, tests["persistence_failed"])
		raw, rpcErr = server.selfEvalStatus(t.Context(), params)
		require.Nil(t, rpcErr)
		self := raw.(map[string]any)
		require.Equal(t, false, self["self_evaluation_available"])
		require.Equal(t, true, self["confidence_assessment_available"])
		require.NotNil(t, self["confidence"])
		require.Nil(t, self["red_team_passed"])
		require.Nil(t, self["iteration"])
		name := "run.json"
		if i == 1 {
			name = "stream.json"
		}
		check := tests["tests"].([]any)[0].(map[string]any)
		require.Equal(t, filepath.Base(name), check["file"])
	}
	server.closeSessionRuntimes()
	reopened, err := session.NewPostgresStore(db)
	require.NoError(t, err)
	cold := NewServer(ServerConfig{Config: cfg, SessionStore: reopened, SessionRuntimeFactory: factory})
	defer cold.closeSessionRuntimes()
	for i, id := range ids {
		params, _ := json.Marshal(map[string]string{"session_id": id})
		raw, rpcErr := cold.testResults(t.Context(), params)
		require.Nil(t, rpcErr)
		encoded, _ := json.Marshal(raw)
		var tests map[string]any
		require.NoError(t, json.Unmarshal(encoded, &tests))
		if i < 2 {
			require.Equal(t, "persisted", tests["snapshot_origin"])
			require.EqualValues(t, 1, tests["passed"])
			require.False(t, tests["running"].(bool))
			raw, rpcErr = cold.selfEvalStatus(t.Context(), params)
			require.Nil(t, rpcErr)
			require.Equal(t, true, raw.(map[string]any)["confidence_assessment_available"])
		} else {
			require.Equal(t, "unobserved", tests["snapshot_origin"])
			require.Empty(t, tests["tests"])
		}
	}
	require.Empty(t, cold.agents, "status reads must not initialize providers or tools")
	// Building a new live agent preserves the prior record and UUID identifiers.
	a, rpcErr := cold.getOrCreateAgent(t.Context(), ids[1])
	require.Nil(t, rpcErr)
	snapshot := a.VerificationSnapshot()
	require.Len(t, snapshot.Tests, 1)
	oldID := snapshot.Tests[0].ID
	// Clear history so the fixture performs another real write/check.
	a.ClearHistory()
	_, err = a.Run(t.Context(), "write additional fixture")
	require.NoError(t, err)
	snapshot = a.VerificationSnapshot()
	require.Len(t, snapshot.Tests, 2)
	require.NotEqual(t, oldID, snapshot.Tests[1].ID)
	require.NoError(t, reopened.Delete(t.Context(), ids[0]))
	data, err := reopened.LoadRuntimeStatus(t.Context(), ids[0])
	require.NoError(t, err)
	require.Empty(t, data, "session deletion must remove private observations")
}

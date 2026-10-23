package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/plan"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
	"github.com/Sahaj-Tech-ltd/overkill/internal/verify"
	"github.com/stretchr/testify/require"
)

type statusJSONVerifier struct {
	root             string
	entered, release chan struct{}
	once             sync.Once
}

func (*statusJSONVerifier) IsWriteTool(name string) bool { return name == "fs" }
func (v *statusJSONVerifier) ExtractWritePaths(_ string, input json.RawMessage) []string {
	var p struct {
		Path string `json:"path"`
	}
	_ = json.Unmarshal(input, &p)
	return []string{filepath.Join(v.root, p.Path)}
}
func (*statusJSONVerifier) AuditTurnPaths([]string) string { return "" }
func (v *statusJSONVerifier) VerifyToolCall(ctx context.Context, name string, input json.RawMessage) string {
	path := v.ExtractWritePaths(name, input)[0]
	if filepath.Ext(path) != ".json" {
		return ""
	} // no checker: must not fabricate a pass
	done := agent.StartVerification(ctx, "json parse", filepath.Base(path))
	if v.entered != nil {
		v.once.Do(func() { close(v.entered) })
		select {
		case <-v.release:
		case <-ctx.Done():
			done("canceled", ctx.Err().Error())
			return ctx.Err().Error()
		}
	}
	ok, detail, skipped := verify.NewJSONVerifier().Verify(ctx, path, nil)
	state := "failed"
	if ok {
		state = "passed"
	}
	if skipped {
		state = "skipped"
	}
	done(state, detail)
	if !ok {
		return detail
	}
	return ""
}
func statusRPC(t *testing.T, ts *httptest.Server, method, id string) map[string]any {
	t.Helper()
	r := readRPCResponse(t, doRPC(t, ts, method, map[string]string{"session_id": id}))
	require.Nil(t, r.Error)
	encoded, err := json.Marshal(r.Result)
	require.NoError(t, err)
	var result map[string]any
	require.NoError(t, json.Unmarshal(encoded, &result))
	return result
}
func TestRuntimeStatusObservesActualJSONChecksAcrossSessions(t *testing.T) {
	db, _ := testpg.Open(t)
	store, err := session.NewPostgresStore(db)
	require.NoError(t, err)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct{ Role, Content string }
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		w.Header().Set("Content-Type", "application/json")
		for _, m := range req.Messages {
			if m.Role == "tool" {
				_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"Finished"},"finish_reason":"stop"}]}`)
				return
			}
		}
		path, content := "passed.json", `{"ok":true}`
		for _, m := range req.Messages {
			if strings.Contains(m.Content, "bad fixture") {
				path, content = "held.json", `{"bad":`
			}
			if strings.Contains(m.Content, "unmatched fixture") {
				path, content = "unmatched.txt", "text"
			}
		}
		args, _ := json.Marshal(map[string]string{"action": "write", "path": path, "content": content})
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"id": "write-check", "type": "function", "function": map[string]string{"name": "fs", "arguments": string(args)}}}}, "finish_reason": "tool_calls"}}})
	}))
	defer provider.Close()
	cfg := config.Default()
	cfg.Agent.DefaultProvider = "fixture"
	cfg.Agent.DefaultModel = "gpt-4o-mini"
	cfg.Providers = []config.ProviderConfig{{Name: "fixture", Type: "openai", BaseURL: provider.URL, APIKey: "test-only"}}
	entered, release := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	var heldID string
	s := NewServer(ServerConfig{Config: cfg, SessionStore: store, SessionRuntimeFactory: func(_ context.Context, m *session.Session) (*SessionRuntime, error) {
		registry := tools.NewRegistry()
		_ = registry.Register(tools.NewFSTool(m.Folder))
		v := &statusJSONVerifier{root: m.Folder}
		if m.ID == heldID {
			v.entered = entered
			v.release = release
		}
		return &SessionRuntime{Tools: registry, PostWriteVerifier: v}, nil
	}})
	defer s.closeSessionRuntimes()
	ts := httptest.NewServer(http.HandlerFunc(s.handleRPC))
	defer ts.Close()
	ids := make([]string, 4)
	for i := range ids {
		m := session.NewSession(t.TempDir())
		ids[i] = m.ID
		require.NoError(t, store.Create(t.Context(), m))
	}
	heldID = ids[0]
	run := func(id, message string) *RPCError {
		p, _ := json.Marshal(map[string]string{"session_id": id, "message": message})
		_, e := s.handleAgentSend(t.Context(), p)
		return e
	}
	finished := make(chan *RPCError, 1)
	go func() { finished <- run(ids[0], "Write bad fixture") }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("actual verifier not reached")
	}
	require.Nil(t, run(ids[1], "Write passing fixture"))
	// A different server can read the durable in-flight observation without
	// pretending it owns the still-live check or declaring that process dead.
	cold := NewServer(ServerConfig{Config: cfg, SessionStore: store})
	coldHTTP := httptest.NewServer(http.HandlerFunc(cold.handleRPC))
	defer coldHTTP.Close()
	historical := statusRPC(t, coldHTTP, "tests.results", ids[0])
	require.Equal(t, "persisted", historical["snapshot_origin"])
	require.Equal(t, false, historical["running"])
	require.EqualValues(t, 1, historical["unconfirmed"])
	require.EqualValues(t, 0, historical["passed"])
	require.Empty(t, cold.agents)
	active := statusRPC(t, ts, "tests.results", ids[0])
	require.Equal(t, true, active["running"])
	require.EqualValues(t, 0, active["passed"])
	passed := statusRPC(t, ts, "tests.results", ids[1])
	require.Equal(t, false, passed["running"])
	require.EqualValues(t, 1, passed["passed"])
	require.EqualValues(t, 0, passed["failed"])
	self := statusRPC(t, ts, "self.eval.status", ids[0])
	require.Equal(t, "post_write_verifier", self["source"])
	require.Equal(t, false, self["self_evaluation_available"])
	require.Nil(t, self["confidence"])
	require.Equal(t, true, self["active"])
	close(release)
	select {
	case e := <-finished:
		require.Nil(t, e)
	case <-time.After(3 * time.Second):
		t.Fatal("held verifier did not finish")
	}
	failed := statusRPC(t, ts, "tests.results", ids[0])
	require.Equal(t, false, failed["running"])
	require.Greater(t, failed["failed"].(float64), float64(0))
	require.EqualValues(t, 0, failed["passed"])
	require.Nil(t, run(ids[2], "Write unmatched fixture"))
	none := statusRPC(t, ts, "tests.results", ids[2])
	require.Empty(t, none["tests"])
	require.EqualValues(t, 0, none["passed"])
	idle := statusRPC(t, ts, "self.eval.status", ids[3])
	require.Equal(t, "idle", idle["status"])
	require.NotContains(t, s.agents, ids[3])
	// Real files were written and parsed, rather than trusting model claims.
	held, err := store.Load(t.Context(), ids[0])
	require.NoError(t, err)
	data, err := os.ReadFile(filepath.Join(held.Folder, "held.json"))
	require.NoError(t, err)
	require.False(t, json.Valid(data))
	for _, method := range []string{"self.eval.status", "tests.results"} {
		for _, payload := range []string{`{}`, `null`, `[]`, `{"session_id":1}`, `{"session_id":"../bad"}`, `{"session_id":"unknown"}`} {
			var rpcErr *RPCError
			if method == "self.eval.status" {
				_, rpcErr = s.handleSelfEvalStatus(t.Context(), []byte(payload))
			} else {
				_, rpcErr = s.handleTestResults(t.Context(), []byte(payload))
			}
			require.NotNil(t, rpcErr)
			require.Equal(t, InvalidParams, rpcErr.Code)
		}
	}
}

func TestDashboardReadsPersistedSelectedSessionWithoutAgent(t *testing.T) {
	db, _ := testpg.Open(t)
	sessions, err := session.NewPostgresStore(db)
	require.NoError(t, err)
	goals, err := agent.NewGoalStore(db)
	require.NoError(t, err)
	dir := t.TempDir()
	ids := make([]string, 2)
	for i := range ids {
		m := session.NewSession(t.TempDir())
		ids[i] = m.ID
		require.NoError(t, sessions.Create(t.Context(), m))
		require.NoError(t, goals.SetWithBudgets(t.Context(), m.ID, "goal "+m.ID, 100+i, 0))
		_, _, _, err := goals.UpdateUsageAtomic(t.Context(), m.ID, 17+i, 3+i)
		require.NoError(t, err)
		_, err = plan.NewStore(dir, m.ID).Set("plan "+m.ID, []string{"real step"})
		require.NoError(t, err)
	}
	s := NewServer(ServerConfig{Config: config.Default(), SessionStore: sessions, DashboardGoalReader: goals.Get, DashboardPlanReader: func(_ context.Context, id string) (*plan.Plan, error) { return plan.NewStore(dir, id).Load() }})
	for index, id := range ids {
		for _, route := range []string{"/api/goal", "/api/plan"} {
			r := httptest.NewRequest("GET", route+"?session_id="+id, nil)
			w := httptest.NewRecorder()
			if route == "/api/goal" {
				s.handleAPIGoal(w, r)
			} else {
				s.handleAPIPlan(w, r)
			}
			require.Equal(t, 200, w.Code)
			require.Contains(t, w.Body.String(), id)
			if route == "/api/goal" {
				var result map[string]any
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
				require.EqualValues(t, 17+index, result["tokens_used"])
				require.EqualValues(t, 100+index, result["token_budget"])
			}
			require.NotContains(t, w.Body.String(), ids[1-index])
			if id == ids[1] {
				require.Contains(t, w.Body.String(), ids[1])
			}
		}
	}
	require.Empty(t, s.agents)
}

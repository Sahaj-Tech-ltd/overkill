package tools

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/acp"
	"github.com/stretchr/testify/require"
)

func TestNamedDurableACPToolsKeepDestinationsAndCredentialsApplicationOwned(t *testing.T) {
	const secret = "private-adapter-test-token"
	var calls atomic.Int64
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer "+secret {
			t.Error("credential not bound by application")
			w.WriteHeader(401)
			return
		}
		if r.URL.Path == "/v1/sessions" {
			_, _ = w.Write([]byte(`[{"id":"session"}]`))
			return
		}
		task := acp.Task{TaskID: "task", MessageID: "task", SessionID: "session", Status: "queued"}
		if r.Method == "POST" && r.URL.Path == "/v1/tasks" {
			var body acp.TaskSubmitRequest
			if json.NewDecoder(r.Body).Decode(&body) != nil || body.IdempotencyKey != "request-key" || body.SessionID != "session" {
				t.Error("wrong receipt body")
				w.WriteHeader(400)
				return
			}
			w.WriteHeader(202)
		}
		if strings.HasSuffix(r.URL.Path, "/await") {
			task.Status = "completed"
			task.Reply = "endpoint ready"
		}
		if strings.HasSuffix(r.URL.Path, "/cancel") {
			task.Status = "cancelled"
		}
		_ = json.NewEncoder(w).Encode(task)
	}))
	defer remote.Close()
	tools := NewACPTaskTools(map[string]ACPRemote{"backend": {URL: remote.URL, Token: secret}})
	require.Len(t, tools, 5)
	for _, tool := range tools {
		metadata := tool.(interface{ Schema() json.RawMessage })
		schema := string(metadata.Schema())
		require.NotContains(t, schema, secret)
		require.NotContains(t, schema, remote.URL)
		require.Contains(t, schema, "backend")
		input := `{"peer":"backend","task_id":"task"}`
		switch tool.Name() {
		case "acp_submit":
			input = `{"peer":"backend","session":"session","content":"implement endpoint","idempotency_key":"request-key"}`
		case "acp_await":
			input = `{"peer":"backend","task_id":"task","wait_ms":20}`
		case "acp_sessions":
			input = `{"peer":"backend"}`
		}
		output, err := tool.Execute(t.Context(), json.RawMessage(input))
		require.NoError(t, err, tool.Name())
		require.NotContains(t, string(output), secret)
		before := calls.Load()
		for _, bad := range []string{`{"peer":"unknown","task_id":"task"}`, `{"peer":"backend","token":"attacker"}`, `{"peer":"backend","agent_url":"http://attacker"}`, `{"peer":"backend","Peer":"backend"}`, `{"peer":"backend","peer":"unknown"}`, `null`, `{"peer":null}`, input + `{}`} {
			_, err = tool.Execute(t.Context(), json.RawMessage(bad))
			require.Error(t, err, "%s %s", tool.Name(), bad)
		}
		require.Equal(t, before, calls.Load(), "invalid authority/input must make no request")
	}
	async := NewACPSendTool(map[string]ACPRemote{"backend": {URL: remote.URL, Token: secret}})
	output, err := async.Execute(t.Context(), json.RawMessage(`{"async":true,"peer":"backend","session":"session","content":"work","idempotency_key":"request-key"}`))
	require.NoError(t, err)
	require.Contains(t, string(output), `"taskID":"task"`)
	_, err = async.Execute(t.Context(), json.RawMessage(`{"async":true,"peer":"backend","agent_url":"http://attacker","session":"session","content":"work","idempotency_key":"request-key"}`))
	require.Error(t, err)

}
func TestNamedACPToolsDoNotForwardCredentialsThroughRedirects(t *testing.T) {
	var received atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { received.Add(1) }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	tools := NewACPTaskTools(map[string]ACPRemote{"backend": {URL: redirect.URL, Token: "bound-token"}})
	for _, tool := range tools {
		if tool.Name() != "acp_status" {
			continue
		}
		_, err := tool.Execute(t.Context(), json.RawMessage(`{"peer":"backend","task_id":"task"}`))
		require.ErrorContains(t, err, "307")
	}
	require.Zero(t, received.Load())
}

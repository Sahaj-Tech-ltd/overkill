package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/coordination"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
	"github.com/stretchr/testify/require"
)

// This is deterministic local-provider acceptance of the composed runtime path.
// The provider chooses actions; every tool effect, message transition and saved
// transcript is produced by the application, never synthesized by this test.
func TestCoordinationComposedFrontendBackendAcceptance(t *testing.T) {
	_, err := exec.LookPath("python3")
	require.NoError(t, err, "composed acceptance requires Python 3 for the real HTTP client")
	scenarioCtx, cancelScenario := context.WithTimeout(t.Context(), 25*time.Second)
	defer cancelScenario()
	database, _ := testpg.Open(t)
	sessions, err := session.NewPostgresStore(database)
	require.NoError(t, err)
	store, err := coordination.NewStore(t.Context(), database)
	require.NoError(t, err)
	var peers []coordination.Peer
	var metadata []*session.Session
	for _, name := range []string{"frontend", "backend"} {
		peer, _, err := store.RegisterPeer(t.Context(), coordination.Peer{ProjectID: "composed", Name: name, Role: name, Workspace: t.TempDir()})
		require.NoError(t, err)
		sess := session.NewSession(peer.Workspace)
		sess.Provider, sess.Model = "fixture", name
		require.NoError(t, sessions.Create(t.Context(), sess))
		require.NoError(t, store.BindSession(t.Context(), peer.ID, sess.ID))
		peers, metadata = append(peers, peer), append(metadata, sess)
	}
	// The endpoint serves the backend's real fs artifact. It does not invent the
	// response in the HTTP handler, so the combined check depends on that write.
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/users" {
			http.NotFound(w, r)
			return
		}
		data, err := os.ReadFile(filepath.Join(metadata[1].Folder, "endpoint.json"))
		if err != nil {
			http.Error(w, "endpoint not built", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(data)
	}))
	defer endpoint.Close()
	backendSpec := coordination.ContractSpec{Name: "users", Endpoint: endpoint.URL + "/users", SchemaVersion: "1", Schema: json.RawMessage(`{"type":"object","required":["users"],"properties":{"users":{"type":"array"}}}`), Workspace: metadata[1].Folder, OwnedPaths: []string{"endpoint.json"}, AcceptanceCriteria: []string{"frontend renders users from live endpoint"}, Artifacts: []coordination.ContractArtifact{{Name: "endpoint response", Path: "endpoint.json", MediaType: "application/json"}}}
	frontendSpec := coordination.ContractSpec{Name: "users UI", Endpoint: "users view", SchemaVersion: "1", Schema: json.RawMessage(`{"type":"object"}`), Workspace: metadata[0].Folder, OwnedPaths: []string{"ui.txt", "client.py", "integration.json"}, AcceptanceCriteria: []string{"independent UI exists and combined endpoint/client check passes"}, Artifacts: []coordination.ContractArtifact{{Name: "combined integration result", Path: "integration.json", MediaType: "application/json"}}}
	v1Published, pinned, revised, atBoundary := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	releaseBoundary := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseBoundary) }) }
	defer release()
	var frontCalls, backCalls atomic.Int32
	var frontID, backID string
	// IDs travel through actual provider tool results, not out-of-band DB writes.
	type providerMessage struct {
		Role       string `json:"role"`
		Content    string `json:"content"`
		ToolCallID string `json:"tool_call_id"`
	}
	var requestID, progressID, changedID string
	var staleReadiness coordination.ContractReadiness
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model    string            `json:"model"`
			Messages []providerMessage `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			http.Error(w, "bad fixture request", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		result := func(id string, target any) {
			for i := len(body.Messages) - 1; i >= 0; i-- {
				if body.Messages[i].ToolCallID == id {
					if err := json.Unmarshal([]byte(body.Messages[i].Content), target); err != nil {
						t.Errorf("tool result %s: %v", id, err)
					}
					return
				}
			}
			t.Errorf("provider did not receive tool result %s", id)
		}
		tool := func(id, name string, args any) {
			encoded, _ := json.Marshal(args)
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"id": id, "type": "function", "function": map[string]any{"name": name, "arguments": string(encoded)}}}}, "finish_reason": "tool_calls"}}})
		}
		stop := func(content string) {
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": content}, "finish_reason": "stop"}}})
		}
		wait := func(ch <-chan struct{}) bool {
			select {
			case <-ch:
				return true
			case <-r.Context().Done():
				return false
			case <-scenarioCtx.Done():
				return false
			}
		}
		contract := func(id, action, cid string, revision int64, spec *coordination.ContractSpec) {
			args := map[string]any{"action": action}
			if cid != "" {
				args["contract_id"] = cid
			}
			if revision > 0 {
				args["expected_revision"] = revision
			}
			if spec != nil {
				args["spec"] = spec
			}
			tool(id, "agent_contract", args)
		}
		write := func(id, path, content string) {
			tool(id, "fs", map[string]any{"action": "write", "path": path, "content": content})
		}
		if body.Model == "backend" {
			switch backCalls.Add(1) {
			case 1:
				contract("back-create", "create", "", 0, &backendSpec)
			case 2:
				var out struct {
					Result coordination.Contract `json:"result"`
				}
				result("back-create", &out)
				backID = out.Result.ID
				write("back-v1", "endpoint.json", `{"users":["Ada"]}`)
			case 3:
				close(v1Published)
				tool("back-progress", "agent_send", map[string]any{"to": "frontend", "kind": "progress", "payload": "endpoint v1 built; reviewing schema", "idempotency_key": "composed-progress"})
			case 4:
				var out struct {
					Message coordination.Message `json:"message"`
				}
				result("back-progress", &out)
				progressID = out.Message.ID
				if !wait(pinned) {
					return
				}
				write("back-v2", "endpoint.json", `{"users":[{"name":"Ada"}],"schema_version":"2"}`)
			case 5:
				backendSpec.SchemaVersion = "2"
				backendSpec.Schema = json.RawMessage(`{"type":"object","required":["users","schema_version"],"properties":{"users":{"type":"array","items":{"type":"object","required":["name"]}},"schema_version":{"const":"2"}}}`)
				backendSpec.CompatibilityChanges = []coordination.CompatibilityChange{{Kind: "breaking", FromVersion: "1", ToVersion: "2", Description: "users entries now carry name; response identifies schema version"}}
				contract("back-update", "update", backID, 1, &backendSpec)
			case 6:
				tool("back-changed", "agent_send", map[string]any{"to": "frontend", "kind": "contract_changed", "payload": map[string]any{"contract_id": backID, "revision": 2, "schema_version": "2"}, "idempotency_key": "composed-schema-change"})
			case 7:
				var out struct {
					Message coordination.Message `json:"message"`
				}
				result("back-changed", &out)
				changedID = out.Message.ID
				close(revised)
				stop("Backend endpoint v2 built and schema change published")
			default:
				stop("Backend has no additional work")
			}
			return
		}
		switch frontCalls.Add(1) {
		case 1:
			contract("front-create", "create", "", 0, &frontendSpec)
		case 2:
			var out struct {
				Result coordination.Contract `json:"result"`
			}
			result("front-create", &out)
			frontID = out.Result.ID
			tool("front-request", "agent_send", map[string]any{"to": "backend", "kind": "request", "payload": "Build GET /users and publish endpoint/schema agreement; report any revision", "idempotency_key": "composed-request"})
		case 3:
			var out struct {
				Message coordination.Message `json:"message"`
			}
			result("front-request", &out)
			requestID = out.Message.ID
			write("front-independent", "ui.txt", "Users view: loading state and title implemented independently\n")
		case 4:
			if !wait(v1Published) {
				return
			}
			contract("front-inspect-v1", "get", backID, 0, nil)
		case 5:
			frontendSpec.Dependencies = []coordination.ContractDependency{{ContractID: backID, Revision: 1}}
			contract("front-pin-v1", "update", frontID, 1, &frontendSpec)
		case 6:
			contract("front-ack-back-v1", "acknowledge", backID, 1, nil)
		case 7:
			contract("front-ack-self-v1", "acknowledge", frontID, 2, nil)
		case 8:
			close(pinned)
			if !wait(revised) {
				return
			}
			contract("front-stale", "readiness", frontID, 0, nil)
		case 9:
			var out struct {
				Result coordination.ContractReadiness `json:"result"`
			}
			result("front-stale", &out)
			staleReadiness = out.Result
			close(atBoundary)
			if !wait(releaseBoundary) {
				return
			}
			stop("Independent UI finished; schema revision queued for next safe turn")
		case 10:
			stop("Backend progress received at a safe turn boundary; await contract_changed")
		case 11:
			// The schema-change event starts a new turn after the interactive turn saved.
			transcript, _ := json.Marshal(body.Messages)
			if !strings.Contains(string(transcript), "bound peer sent this coordination message") || !strings.Contains(body.Messages[len(body.Messages)-1].Content, "contract_changed") {
				t.Error("queued delivery did not use production coordination envelope")
			}
			contract("front-inspect-v2", "get", backID, 0, nil)
		case 12:
			var out struct {
				Result coordination.Contract `json:"result"`
			}
			result("front-inspect-v2", &out)
			if out.Result.Revision != 2 || out.Result.Spec.SchemaVersion != "2" {
				t.Error("frontend did not re-read current backend agreement")
			}
			frontendSpec.Dependencies = []coordination.ContractDependency{{ContractID: backID, Revision: 2}}
			contract("front-pin-v2", "update", frontID, 2, &frontendSpec)
		case 13:
			contract("front-ack-back-v2", "acknowledge", backID, 2, nil)
		case 14:
			contract("front-ack-self-v2", "acknowledge", frontID, 3, nil)
		case 15:
			client := fmt.Sprintf("import json, urllib.request\nwith urllib.request.urlopen(%q, timeout=5) as r:\n    data=json.load(r)\nassert data['schema_version']=='2', data\nassert data['users']==[{'name':'Ada'}], data\nresult={'passed':True,'schema_version':data['schema_version'],'rendered':[u['name'] for u in data['users']]}\nwith open('integration.json','w') as f: json.dump(result,f)\nprint(json.dumps(result))\n", endpoint.URL+"/users")
			write("front-client", "client.py", client)
		case 16:
			tool("front-combined-check", "shell", map[string]any{"command": "python3 client.py", "working_dir": metadata[0].Folder, "timeout_seconds": 10})
		case 17:
			var out tools.ShellOutput
			result("front-combined-check", &out)
			if out.ExitCode != 0 || !out.Completed || !strings.Contains(out.Stdout, `"passed": true`) {
				t.Errorf("combined integration did not pass: %+v", out)
			}
			tool("front-read-evidence", "fs", map[string]any{"action": "read", "path": "integration.json"})
		case 18:
			stop("Frontend integrated backend schema v2; actual combined check passed; evidence integration.json")
		default:
			stop("Queued backend update observed after integration")
		}
	}))
	defer provider.Close()
	cfg := coordinationConfig(provider.URL)
	cfg.Agent.MaxTurns = 40
	server := NewServer(ServerConfig{Config: cfg, SessionStore: sessions, CoordStore: store, SessionRuntimeFactory: func(ctx context.Context, metadata *session.Session) (*SessionRuntime, error) {
		peer, err := store.PeerForSession(ctx, metadata.ID)
		if err != nil {
			return nil, err
		}
		registry := tools.NewRegistry()
		registry.SetExecutionGuard(tools.NewCoordinationExecutionGuard(store, peer.ID, peer.Workspace))
		all := append(tools.NewCoordinationTools(store, peer.ID), tools.NewCoordinationContractTool(store, peer.ID), tools.NewFSTool(metadata.Folder), tools.NewShellTool())
		for _, tool := range all {
			if err := registry.Register(tool); err != nil {
				return nil, err
			}
		}
		return &SessionRuntime{Tools: registry}, nil
	}})
	startHTTPServer(t, server)
	defer server.Close()
	defer cancelScenario()
	defer release()
	// Use the authenticated production RPC ingress for the real frontend turn.
	interactiveDone := make(chan Response, 1)
	go func() {
		interactiveDone <- sidebarRPC(t, server, "agent.send", map[string]any{"session_id": metadata[0].ID, "message": "Implement users UI; request backend interface, continue independent work, then integrate the current schema."})
	}()
	select {
	case <-atBoundary:
	case <-time.After(20 * time.Second):
		t.Fatal("composed scenario did not reach frontend turn boundary")
	}
	require.False(t, staleReadiness.Ready)
	require.Contains(t, fmt.Sprint(staleReadiness.Blockers), "dependency_revision_changed")
	require.Never(t, func() bool { return frontCalls.Load() != 9 }, 600*time.Millisecond, 20*time.Millisecond, "queued delivery must not reenter the active frontend turn across dispatcher polls")
	for _, id := range []string{progressID, changedID} {
		message, err := store.Read(t.Context(), peers[0].ID, id)
		require.NoError(t, err)
		require.Equal(t, coordination.Accepted, message.State, "queued peer event cannot execute inside interactive turn")
		require.Equal(t, requestID, message.ParentMessageID, "backend events inherit executing request lineage")
	}
	independent, err := os.ReadFile(filepath.Join(metadata[0].Folder, "ui.txt"))
	require.NoError(t, err)
	require.Contains(t, string(independent), "implemented independently")
	require.Eventually(t, func() bool {
		m, err := store.Read(t.Context(), peers[0].ID, requestID)
		return err == nil && m.State == coordination.Completed
	}, 5*time.Second, 20*time.Millisecond)
	release()
	select {
	case response := <-interactiveDone:
		require.Nil(t, response.Error)
	case <-time.After(10 * time.Second):
		t.Fatal("interactive frontend did not finish")
	}
	evidence := filepath.Join(metadata[0].Folder, "integration.json")
	require.Eventually(t, func() bool { data, err := os.ReadFile(evidence); return err == nil && json.Valid(data) }, 15*time.Second, 20*time.Millisecond)
	require.Eventually(t, func() bool {
		m, err := store.Read(t.Context(), peers[0].ID, changedID)
		return err == nil && m.State == coordination.Completed
	}, 10*time.Second, 20*time.Millisecond)
	require.Eventually(t, func() bool {
		var active int
		err := database.QueryRowContext(t.Context(), `SELECT count(*) FROM coordination_messages WHERE state IN ('accepted','started')`).Scan(&active)
		return err == nil && active == 0
	}, 8*time.Second, 20*time.Millisecond, "all bounded scenario deliveries, including the automatic answer, must drain")
	server.Close()
	// Fresh store instances and a fresh API server have no in-memory agent cache.
	freshStore, err := coordination.NewStore(t.Context(), database)
	require.NoError(t, err)
	freshSessions, err := session.NewPostgresStore(database)
	require.NoError(t, err)
	fresh := NewServer(ServerConfig{Config: cfg, SessionStore: freshSessions, CoordStore: freshStore})
	defer fresh.Close()
	for _, i := range []int{0, 1} {
		response, rpcErr := fresh.handleSessionMessages(t.Context(), coordInputForRuntime(t, map[string]any{"id": metadata[i].ID}))
		require.Nil(t, rpcErr)
		require.NotNil(t, response)
		require.Empty(t, fresh.agents, "reading durable messages must not recreate an executing agent")
		loaded, err := freshSessions.Load(t.Context(), metadata[i].ID)
		require.NoError(t, err)
		history, err := json.Marshal(loaded.Messages)
		require.NoError(t, err)
		if i == 0 {
			require.Contains(t, string(history), "front-combined-check")
			var combined tools.ShellOutput
			found := false
			for _, message := range loaded.Messages {
				if message.ToolCallID == "front-combined-check" {
					require.NoError(t, json.Unmarshal([]byte(message.Content), &combined))
					found = true
				}
			}
			require.True(t, found)
			require.Zero(t, combined.ExitCode)
			require.Contains(t, combined.Stdout, `"passed": true`)
			require.Contains(t, string(history), "front-stale")
		} else {
			require.Contains(t, string(history), "back-v2")
			require.Contains(t, string(history), "back-changed")
		}
	}
	ready, err := freshStore.ContractReadiness(t.Context(), peers[0].ID, frontID)
	require.NoError(t, err)
	require.True(t, ready.Ready, "%+v", ready.Blockers)
	frontCurrent, err := freshStore.ReadContract(t.Context(), peers[0].ID, frontID)
	require.NoError(t, err)
	require.EqualValues(t, 3, frontCurrent.Revision)
	require.Equal(t, []coordination.ContractDependency{{ContractID: backID, Revision: 2}}, frontCurrent.Spec.Dependencies)
	old, err := freshStore.ContractRevision(t.Context(), peers[0].ID, backID, 1)
	require.NoError(t, err)
	require.Equal(t, "1", old.SchemaVersion)
	current, err := freshStore.ReadContract(t.Context(), peers[0].ID, backID)
	require.NoError(t, err)
	require.EqualValues(t, 2, current.Revision)
	require.Equal(t, "breaking", current.Spec.CompatibilityChanges[0].Kind)
	var answerID, answerParent, answerReply string
	require.NoError(t, database.QueryRowContext(t.Context(), `SELECT id,parent_message_id,in_reply_to FROM coordination_messages WHERE kind='answer' AND in_reply_to=$1`, requestID).Scan(&answerID, &answerParent, &answerReply))
	require.Equal(t, requestID, answerParent)
	require.Equal(t, requestID, answerReply)
	persisted, err := freshStore.Read(t.Context(), peers[0].ID, changedID)
	require.NoError(t, err)
	require.Equal(t, coordination.Completed, persisted.State)
	usage, err := freshStore.UsageForMessage(t.Context(), peers[0].ID, requestID)
	require.NoError(t, err)
	require.Positive(t, usage.UnknownUsage, "the fixture omits provider usage; absent tokens must remain explicitly unknown")
	data, err := os.ReadFile(evidence)
	require.NoError(t, err)
	require.JSONEq(t, `{"passed":true,"schema_version":"2","rendered":["Ada"]}`, string(data))
	// Optional opt-in export keeps actual produced evidence after TempDir and
	// isolated PostgreSQL cleanup; normal test runs have no persistent output.
	if destination := os.Getenv("OVERKILL_COORD_ACCEPTANCE_ARTIFACT_DIR"); destination != "" {
		require.NoError(t, os.MkdirAll(destination, 0o700))
		for i, names := range [][]string{{"ui.txt", "client.py", "integration.json"}, {"endpoint.json"}} {
			for _, name := range names {
				artifact, err := os.ReadFile(filepath.Join(metadata[i].Folder, name))
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(filepath.Join(destination, name), artifact, 0o600))
			}
			loaded, err := freshSessions.Load(t.Context(), metadata[i].ID)
			require.NoError(t, err)
			transcript, err := json.MarshalIndent(loaded.Messages, "", "  ")
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(destination, []string{"frontend-transcript.json", "backend-transcript.json"}[i]), transcript, 0o600))
		}
		manifest, err := json.MarshalIndent(map[string]any{"scenario": t.Name(), "usage_journal": usage, "backend_current": current, "frontend_current": frontCurrent, "backend_original": old, "frontend_readiness": ready, "stale_readiness": staleReadiness, "request_id": requestID, "progress_id": progressID, "contract_changed": persisted, "answer_id": answerID, "answer_parent": answerParent, "answer_in_reply_to": answerReply}, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(destination, "manifest.json"), manifest, 0o600))
	}
	t.Log("Artifacts verified: backend endpoint.json; frontend ui.txt, client.py, integration.json; durable contracts v1/v2, frontend dependency revision 2, message lineage and both session transcripts")
}

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/api"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/coordination"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
	"github.com/stretchr/testify/require"
)

func contractRuntimePair(t *testing.T, shared bool) (*runtimeServices, []*session.Session, []*api.SessionRuntime) {
	t.Helper()
	database, _ := testpg.Open(t)
	store, err := session.NewPostgresStore(database)
	require.NoError(t, err)
	services, err := newRuntimeServices(config.Default(), database, t.TempDir())
	require.NoError(t, err)
	front, back := t.TempDir(), t.TempDir()
	if shared {
		back = front
	}
	sessions := []*session.Session{session.NewSession(front), session.NewSession(back)}
	runtimes := make([]*api.SessionRuntime, 0, 2)
	for i, name := range []string{"frontend", "backend"} {
		require.NoError(t, store.Create(t.Context(), sessions[i]))
		peer, _, err := services.Coordination.RegisterPeer(t.Context(), coordination.Peer{ProjectID: "contract-fixture", Name: name, Role: name, Workspace: sessions[i].Folder})
		require.NoError(t, err)
		require.NoError(t, services.Coordination.BindSession(t.Context(), peer.ID, sessions[i].ID))
		runtime, err := services.BuildSession(t.Context(), sessions[i])
		require.NoError(t, err)
		t.Cleanup(runtime.Cleanup)
		runtimes = append(runtimes, runtime)
	}
	return services, sessions, runtimes
}

func runtimeContract(t *testing.T, runtime *api.SessionRuntime, action string, contract coordination.Contract, spec *coordination.ContractSpec, result any) {
	t.Helper()
	input := map[string]any{"action": action}
	if contract.ID != "" {
		input["contract_id"], input["expected_revision"] = contract.ID, contract.Revision
	}
	if spec != nil {
		input["spec"] = spec
	}
	data, err := runtimeTool(runtime.Tools, "agent_contract", input)
	require.NoError(t, err)
	if result != nil {
		var envelope struct {
			Result json.RawMessage `json:"result"`
		}
		require.NoError(t, json.Unmarshal(data, &envelope))
		require.NoError(t, json.Unmarshal(envelope.Result, result))
	}
}

func fixtureContractSpec(name, workspace, owned string) coordination.ContractSpec {
	return coordination.ContractSpec{Name: name, Endpoint: "GET /users", SchemaVersion: "1", Schema: json.RawMessage(`{"field":"name"}`), Workspace: workspace, OwnedPaths: []string{owned}, AcceptanceCriteria: []string{"A real client validates the endpoint response."}}
}

func TestCoordinationContractsGuardActualSharedWorkspaceMutations(t *testing.T) {
	_, sessions, runtimes := contractRuntimePair(t, true)
	frontSpec := fixtureContractSpec("frontend", sessions[0].Folder, "frontend")
	backSpec := fixtureContractSpec("backend", sessions[1].Folder, "backend")
	var front, back coordination.Contract
	runtimeContract(t, runtimes[0], "create", coordination.Contract{}, &frontSpec, &front)
	runtimeContract(t, runtimes[1], "create", coordination.Contract{}, &backSpec, &back)
	_, err := runtimeTool(runtimes[1].Tools, "fs", map[string]string{"action": "write", "path": "backend/api.json", "content": "backend-owned"})
	require.NoError(t, err)
	for _, call := range []struct {
		name  string
		input any
	}{
		{"fs", map[string]string{"action": "write", "path": "backend/api.json", "content": "clobbered"}},
		{"fs", map[string]string{"action": "edit", "path": "backend/api.json", "old": "backend-owned", "new": "clobbered"}},
		{"fs", map[string]string{"action": "mkdir", "path": "backend/new"}},
		{"patch", map[string]string{"path": "backend/api.json", "patch": "@@ -1 +1 @@\n-backend-owned\n+clobbered\n"}},
		{"fs", map[string]string{"action": "write", "path": "unclaimed.txt", "content": "unclaimed"}},
		{"shell", map[string]any{"command": "printf unexpected > backend/opaque.txt", "timeout_seconds": 5}},
	} {
		_, err := runtimeTool(runtimes[0].Tools, call.name, call.input)
		require.ErrorIs(t, err, coordination.ErrOwnershipConflict, call.name)
	}
	content, err := os.ReadFile(filepath.Join(sessions[0].Folder, "backend/api.json"))
	require.NoError(t, err)
	require.Equal(t, "backend-owned", string(content))
	_, err = os.Stat(filepath.Join(sessions[0].Folder, "backend/opaque.txt"))
	require.ErrorIs(t, err, os.ErrNotExist)
	_, err = runtimeTool(runtimes[0].Tools, "fs", map[string]string{"action": "read", "path": "backend/api.json"})
	require.NoError(t, err, "corresponding agents can inspect shared interface artifacts")
	_, err = runtimeTool(runtimes[0].Tools, "fs", map[string]string{"action": "write", "path": "frontend/client.txt", "content": "frontend-owned"})
	require.NoError(t, err)
	if err := os.Symlink(filepath.Join(sessions[0].Folder, "backend"), filepath.Join(sessions[0].Folder, "alias")); err == nil {
		_, err = runtimeTool(runtimes[0].Tools, "fs", map[string]string{"action": "write", "path": "alias/api.json", "content": "alias overwrite"})
		require.ErrorContains(t, err, "symbolic links")
	}
}

// A deterministic client/endpoint fixture exercises the production runtime's
// actual tools and durable correspondence. It is not an autonomous-model or
// paid-provider acceptance claim.
func TestCoordinationContractSchemaChangeBuildsActualClient(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		if os.Getenv("OVERKILL_REQUIRE_FRONTEND_TEST") == "1" {
			t.Fatal(err)
		}
		t.Skip("Node required for real client acceptance")
	}
	services, sessions, runtimes := contractRuntimePair(t, false)
	apiFile := filepath.Join(sessions[1].Folder, "backend/api-response.json")
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := os.ReadFile(apiFile)
		if err != nil {
			http.Error(w, "endpoint fixture unavailable", 503)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(data)
	}))
	defer endpoint.Close()
	backSpec := fixtureContractSpec("users-api", sessions[1].Folder, "backend")
	backSpec.Endpoint = endpoint.URL + "/users"
	var back, front coordination.Contract
	runtimeContract(t, runtimes[1], "create", coordination.Contract{}, &backSpec, &back)
	frontSpec := fixtureContractSpec("users-client", sessions[0].Folder, "frontend")
	frontSpec.Dependencies = []coordination.ContractDependency{{ContractID: back.ID, Revision: back.Revision}}
	runtimeContract(t, runtimes[0], "create", coordination.Contract{}, &frontSpec, &front)
	runtimeContract(t, runtimes[0], "acknowledge", back, nil, nil)
	runtimeContract(t, runtimes[0], "acknowledge", front, nil, nil)
	var readiness coordination.ContractReadiness
	runtimeContract(t, runtimes[0], "readiness", front, nil, &readiness)
	require.True(t, readiness.Ready, "%+v", readiness.Blockers)
	write := func(runtime *api.SessionRuntime, path, content string) {
		_, err := runtimeTool(runtime.Tools, "fs", map[string]string{"action": "write", "path": path, "content": content})
		require.NoError(t, err)
	}
	buildClient := func(field string) {
		urlJSON, _ := json.Marshal(endpoint.URL + "/users")
		fieldJSON, _ := json.Marshal(field)
		code := fmt.Sprintf("const r=await fetch(%s);if(!r.ok)throw Error('endpoint failed');const body=await r.json();if(body.users[0][%s]!=='Ada')throw Error('contract mismatch');console.log('CLIENT_CONTRACT_ACCEPTED');\n", urlJSON, fieldJSON)
		write(runtimes[0], "frontend/client.mjs", code)
		quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
		output, err := runtimeTool(runtimes[0].Tools, "shell", map[string]any{"command": quote(node) + " frontend/client.mjs", "timeout_seconds": 10})
		require.NoError(t, err)
		var check struct {
			ExitCode int    `json:"exit_code"`
			Stdout   string `json:"stdout"`
		}
		require.NoError(t, json.Unmarshal(output, &check))
		require.Zero(t, check.ExitCode, string(output))
		require.Contains(t, check.Stdout, "CLIENT_CONTRACT_ACCEPTED")
	}
	write(runtimes[1], "backend/api-response.json", `{"users":[{"name":"Ada"}]}`)
	buildClient("name")
	backSpec.SchemaVersion = "2"
	backSpec.Schema = json.RawMessage(`{"field":"display_name"}`)
	backSpec.CompatibilityChanges = []coordination.CompatibilityChange{{Kind: "breaking", FromVersion: "1", ToVersion: "2", Description: "name is replaced by display_name"}}
	runtimeContract(t, runtimes[1], "update", back, &backSpec, &back)
	write(runtimes[1], "backend/api-response.json", `{"users":[{"display_name":"Ada"}]}`)
	_, err = runtimeTool(runtimes[1].Tools, "agent_send", map[string]any{"to": "frontend", "kind": "contract_changed", "payload": map[string]any{"contract_id": back.ID, "revision": back.Revision}, "idempotency_key": "schema-v2"})
	require.NoError(t, err)
	inbox, err := runtimeTool(runtimes[0].Tools, "agent_inbox", map[string]any{})
	require.NoError(t, err)
	require.Contains(t, string(inbox), "contract_changed")
	runtimeContract(t, runtimes[0], "readiness", front, nil, &readiness)
	require.False(t, readiness.Ready, "schema changes must invalidate prior agreement")
	frontSpec.SchemaVersion = "2"
	frontSpec.Schema = json.RawMessage(`{"field":"display_name"}`)
	frontSpec.CompatibilityChanges = backSpec.CompatibilityChanges
	frontSpec.Dependencies[0].Revision = back.Revision
	runtimeContract(t, runtimes[0], "update", front, &frontSpec, &front)
	runtimeContract(t, runtimes[0], "acknowledge", back, nil, nil)
	runtimeContract(t, runtimes[0], "acknowledge", front, nil, nil)
	runtimeContract(t, runtimes[0], "readiness", front, nil, &readiness)
	require.True(t, readiness.Ready, "%+v", readiness.Blockers)
	buildClient("display_name")
	peer, err := services.Coordination.PeerForSession(t.Context(), sessions[0].ID)
	require.NoError(t, err)
	reopened, err := coordination.NewStore(t.Context(), services.database)
	require.NoError(t, err)
	persisted, err := reopened.ReadContract(t.Context(), peer.ID, front.ID)
	require.NoError(t, err)
	require.Equal(t, back.Revision, persisted.Spec.Dependencies[0].Revision)
}

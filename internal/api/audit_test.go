package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/audit"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
	"github.com/stretchr/testify/require"
)

func TestAuditSessionRPCRequiresAuthBaselineAndExecutesRealChecks(t *testing.T) {
	t.Setenv("PATH", filepath.Join(runtime.GOROOT(), "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module auditfixture\n\ngo 1.23\n"), 0600))
	source := filepath.Join(dir, "calc.go")
	require.NoError(t, os.WriteFile(source, []byte("package calc\nfunc Add(a,b int)int{return a+b}\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "calc_test.go"), []byte("package calc\nimport \"testing\"\nfunc TestSum(t *testing.T){if Add(1,2)!=3{t.Fatal(\"bad sum\")}}\n"), 0600))
	metadata := session.NewSession(dir)
	other := session.NewSession(t.TempDir())
	store := &mockSessionStore{sessions: map[string]*session.Session{metadata.ID: metadata, other.ID: other}}
	cfg := config.Default()
	cfg.Agent.DefaultProvider = "fixture"
	cfg.Agent.DefaultModel = "fixture-model"
	cfg.Providers = []config.ProviderConfig{{Name: "fixture", Type: "openai", APIKey: "test-only", Models: []config.ModelConfig{{ID: "fixture-model"}}}}
	s := NewServer(ServerConfig{Config: cfg, SessionStore: store, SessionRuntimeFactory: func(_ context.Context, m *session.Session) (*SessionRuntime, error) {
		registry := tools.NewRegistry()
		require.NoError(t, registry.Register(tools.NewFSTool(m.Folder)))
		return &SessionRuntime{Tools: registry, CompletionAuditor: &audit.Auditor{WorkDir: m.Folder}}, nil
	}})
	startHTTPServer(t, s)
	payload := `{"jsonrpc":"2.0","id":1,"method":"session.audit","params":{"session_id":"` + metadata.ID + `","action":"snapshot"}}`
	response, err := http.Post(s.Addr()+"/rpc", "application/json", strings.NewReader(payload))
	require.NoError(t, err)
	response.Body.Close()
	require.Equal(t, http.StatusUnauthorized, response.StatusCode)
	params := map[string]any{"session_id": metadata.ID, "action": "run", "claims": []audit.Claim{{Description: "updated calculator", Files: []string{"calc.go"}, BuildPass: true, TestsPass: true}}}
	before := sidebarRPC(t, s, "session.audit", params)
	require.NotNil(t, before.Error)
	require.Contains(t, before.Error.Message, "baseline")
	captured := sidebarRPC(t, s, "session.audit", map[string]string{"session_id": metadata.ID, "action": "snapshot"})
	require.Nil(t, captured.Error)
	require.NoError(t, os.WriteFile(source, []byte("package calc\nfunc Add(a,b int)int{return a+b+0}\n"), 0600))
	result := sidebarRPC(t, s, "session.audit", params)
	require.Nil(t, result.Error)
	data, err := json.Marshal(result.Result)
	require.NoError(t, err)
	var report audit.Report
	require.NoError(t, json.Unmarshal(data, &report))
	require.True(t, report.Passed, "%+v", report.Findings)
	require.Len(t, report.Checks, 2)
	for _, check := range report.Checks {
		require.Equal(t, "passed", check.Status)
		require.Equal(t, "go", check.Language)
	}
	params["session_id"] = other.ID
	wrong := sidebarRPC(t, s, "session.audit", params)
	require.NotNil(t, wrong.Error)
	require.Contains(t, wrong.Error.Message, "baseline")
}

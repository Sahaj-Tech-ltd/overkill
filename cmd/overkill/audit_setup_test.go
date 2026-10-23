package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/stretchr/testify/require"
)

func TestAuditProductionWorkerActuallyReadsSearchesAndInspectsDiff(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		if os.Getenv("OVERKILL_REQUIRE_AUDIT_RUNTIME") == "1" {
			t.Fatal(err)
		}
		t.Skip(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "source.ts")
	require.NoError(t, os.WriteFile(path, []byte("export const before = 1;\n"), 0600))
	for _, args := range [][]string{{"init"}, {"add", "source.ts"}, {"-c", "user.name=Audit Fixture", "-c", "user.email=audit@example.invalid", "-c", "core.hooksPath=/dev/null", "commit", "-m", "baseline"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))
	}
	require.NoError(t, os.WriteFile(path, []byte("export const after = 2;\n"), 0600))
	var requests atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Tools []struct {
				Function struct {
					Name       string
					Parameters json.RawMessage
				}
			}
			Messages []struct{ Role, Content string }
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		names := []string{}
		for _, tool := range body.Tools {
			names = append(names, tool.Function.Name)
			if tool.Function.Name == "fs" {
				require.NotContains(t, string(tool.Function.Parameters), "write")
			}
		}
		require.ElementsMatch(t, []string{"fs", "audit_search", "audit_git"}, names)
		step := requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		tool, args := "", ""
		switch step {
		case 1:
			tool, args = "fs", `{"action":"read","path":"source.ts"}`
		case 2:
			require.Contains(t, body.Messages[len(body.Messages)-1].Content, "export const after = 2")
			tool, args = "audit_search", `{"pattern":"after"}`
		case 3:
			require.Contains(t, body.Messages[len(body.Messages)-1].Content, "source.ts")
			require.Contains(t, body.Messages[len(body.Messages)-1].Content, `"line":1`)
			tool, args = "audit_git", `{"action":"diff"}`
		case 4:
			require.Contains(t, body.Messages[len(body.Messages)-1].Content, "+export const after = 2")
		default:
			t.Errorf("unexpected call %d", step)
		}
		message := map[string]any{"role": "assistant"}
		finish := "stop"
		if tool != "" {
			finish = "tool_calls"
			message["tool_calls"] = []any{map[string]any{"id": "verified", "type": "function", "function": map[string]string{"name": tool, "arguments": args}}}
		} else {
			message["content"] = `{"findings":[{"claim":"renamed export","verdict":"ok","evidence":"read/search/diff show after export"}]}`
		}
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"id": "fixture", "choices": []any{map[string]any{"message": message, "finish_reason": finish}}, "usage": map[string]int{"prompt_tokens": 5, "completion_tokens": 2}}))
	}))
	defer provider.Close()
	cfg := config.Default()
	cfg.Agent.DefaultProvider = "audit-provider"
	cfg.Agent.DefaultModel = "audit-model"
	cfg.Providers = []config.ProviderConfig{{Name: "audit-provider", Type: "custom", APIKey: "test-only", BaseURL: provider.URL, Models: []config.ModelConfig{{ID: "audit-model"}}}}
	auditor, cleanup := newCompletionAuditor(cfg, dir)
	defer cleanup()
	summary, err := auditor.SubAgent.Run(context.Background(), "Verify renamed export")
	require.NoError(t, err)
	require.Contains(t, summary, "read/search/diff")
	require.EqualValues(t, 4, requests.Load())
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "export const after = 2;\n", string(data))
}

func TestAuditProductionWorkerCannotWrite(t *testing.T) {
	dir := t.TempDir()
	var requests atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if requests.Add(1) == 1 {
			_, _ = w.Write([]byte(`{"id":"write","choices":[{"message":{"role":"assistant","tool_calls":[{"id":"write","type":"function","function":{"name":"fs","arguments":"{\"action\":\"write\",\"path\":\"forbidden.txt\",\"content\":\"bad\"}"}}]},"finish_reason":"tool_calls"}]}`))
		} else {
			_, _ = w.Write([]byte(`{"id":"done","choices":[{"message":{"role":"assistant","content":"{\"findings\":[]}"},"finish_reason":"stop"}]}`))
		}
	}))
	defer provider.Close()
	cfg := config.Default()
	cfg.Agent.DefaultModel = "audit-model"
	cfg.Providers = []config.ProviderConfig{{Name: "audit-provider", Type: "custom", APIKey: "test-only", BaseURL: provider.URL, Models: []config.ModelConfig{{ID: "audit-model"}}}}
	auditor, cleanup := newCompletionAuditor(cfg, dir)
	defer cleanup()
	_, err := auditor.SubAgent.Run(t.Context(), "verify claims")
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "read-only"), err)
	_, err = os.Stat(filepath.Join(dir, "forbidden.txt"))
	require.True(t, os.IsNotExist(err))
}

func TestAuditCLICommandCapturesAndRunsLanguageChecks(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"unverified"}`), 0600))
	auditor, _ := newCompletionAuditor(config.Default(), dir)
	// No provider should be invoked when merely capturing a baseline.
	require.NoError(t, runAuditCommand(t.Context(), auditor, "start"))
	auditor.SubAgent = nil
	// Running explicit claims reports unavailable scripts instead of silently
	// turning a language mismatch into a successful Go verification.
	require.ErrorContains(t, runAuditCommand(t.Context(), auditor, `run [{"description":"updated package","files":["package.json"]}]`), "completion audit failed")
	require.Error(t, runAuditCommand(t.Context(), auditor, "run invalid-json"))
	require.Error(t, runAuditCommand(t.Context(), auditor, ""))
}

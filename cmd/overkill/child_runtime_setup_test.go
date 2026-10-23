package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/subagent"
)

func TestChildProviderUsesConfiguredEndpointHeadersAndModel(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("X-Child-Config") != "preserved" || r.Header.Get("Authorization") != "Bearer configured-key" {
			t.Errorf("provider configuration lost")
		}
		var body struct{ Model string }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Model != "configured-model" {
			t.Errorf("incorrect request model: %+v %v", body, err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"response","choices":[{"message":{"role":"assistant","content":"configured response"},"finish_reason":"stop"}],"usage":{"prompt_tokens":8,"completion_tokens":4}}`))
	}))
	defer server.Close()
	cfg := config.Default()
	cfg.Providers = []config.ProviderConfig{{Name: "private-provider", Type: "custom", APIKey: "configured-key", BaseURL: server.URL, Headers: map[string]string{"X-Child-Config": "preserved"}, Models: []config.ModelConfig{{ID: "configured-model", CostIn: 2, CostOut: 4}}}}
	cfg.Agent.DefaultProvider = "private-provider"
	cfg.Agent.DefaultModel = "configured-model"
	worker, err := childWorkerFactory(cfg, t.TempDir())(subagent.RealWorkerConfig{Goal: "answer from supplied context", MaxSteps: 1})
	if err != nil {
		t.Fatal(err)
	}
	result, err := worker.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || result.Summary != "configured response" || result.TokensIn != 8 || result.TokensOut != 4 {
		t.Fatalf("configured child did not run: %+v calls=%d", result, calls.Load())
	}
}

func TestChildProviderRejectsUnknownRoutesWithoutFallback(t *testing.T) {
	cfg := config.Default()
	cfg.Providers = []config.ProviderConfig{{Name: "configured", Type: "custom", APIKey: "key", BaseURL: "http://localhost", Models: []config.ModelConfig{{ID: "known"}}}}
	for _, route := range []struct{ provider, model string }{{"unknown", "known"}, {"configured", "unknown"}, {"", "unknown"}} {
		if _, _, err := resolveChildProvider(cfg, route.provider, route.model); err == nil {
			t.Fatalf("unsupported route accepted: %+v", route)
		}
	}
	cfg.Providers[0].APIKey = ""
	t.Setenv("CONFIGURED_API_KEY", "")
	if key := providers.CanonicalAPIKeyEnv("custom"); key != "" {
		t.Setenv(key, "")
	}
	if _, _, err := resolveChildProvider(cfg, "configured", "known"); err == nil || !strings.Contains(err.Error(), "API key") {
		t.Fatalf("missing credentials not reported: %v", err)
	}
}

func TestChildProviderPinnedModelSelectsItsConfiguredProvider(t *testing.T) {
	cfg := config.Default()
	cfg.Agent.DefaultProvider = "default"
	cfg.Providers = []config.ProviderConfig{{Name: "default", Type: "custom", APIKey: "default-key", BaseURL: "http://localhost", Models: []config.ModelConfig{{ID: "first"}}}, {Name: "other", Type: "custom", APIKey: "other-key", BaseURL: "http://localhost", Models: []config.ModelConfig{{ID: "second"}}}}
	provider, model, err := resolveChildProvider(cfg, "", "second")
	if err != nil {
		t.Fatal(err)
	}
	if provider.Name() != "other" || model != "second" {
		t.Fatalf("pinned model went to wrong provider: %s %s", provider.Name(), model)
	}
}

func TestChildRuntimeFactoryPropagatesRealProviderUsage(t *testing.T) {
	// Provider configuration prices remain available for metering; this checks
	// construction without performing an external network request.
	cfg := config.Default()
	cfg.Providers = []config.ProviderConfig{{Name: "configured", Type: "custom", APIKey: "key", BaseURL: "http://localhost", Models: []config.ModelConfig{{ID: "known", CostIn: 2, CostOut: 4}}}}
	provider, _, err := resolveChildProvider(cfg, "configured", "known")
	if err != nil {
		t.Fatal(err)
	}
	var model providers.Model
	for _, candidate := range provider.Models() {
		if candidate.ID == "known" {
			model = candidate
		}
	}
	if model.CostIn != 2 || model.CostOut != 4 || !model.SupportsTools {
		t.Fatalf("model configuration lost: %+v", model)
	}
}

func TestChildDelegationActuallyCreatesWorkspaceFile(t *testing.T) {
	dir := t.TempDir()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Decode the OpenAI wire fields rather than the internal Request struct.
		var wire struct {
			Tools []json.RawMessage `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&wire); err != nil {
			t.Error(err)
		}
		if len(wire.Tools) != 2 {
			t.Errorf("delegated toolset was lost: %d tools", len(wire.Tools))
		}
		w.Header().Set("Content-Type", "application/json")
		if calls.Add(1) == 1 {
			_, _ = w.Write([]byte(`{"id":"first","choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"id":"write","type":"function","function":{"name":"fs","arguments":"{\"action\":\"write\",\"path\":\"child.txt\",\"content\":\"genuine child output\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":3,"completion_tokens":2}}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"second","choices":[{"message":{"role":"assistant","content":"child.txt written"},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":1}}`))
	}))
	defer server.Close()
	cfg := config.Default()
	cfg.Providers = []config.ProviderConfig{{Name: "configured", Type: "custom", APIKey: "key", BaseURL: server.URL, Models: []config.ModelConfig{{ID: "test"}}}}
	manager := subagent.NewManager(subagent.Config{})
	manager.SetWorkerFactory(childWorkerFactory(cfg, dir))
	results, err := manager.SpawnDecomposed(context.Background(), "write child.txt in the workspace", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Status != "completed" || len(results[0].FilesWritten) != 1 || results[0].TokensIn != 7 || results[0].TokensOut != 3 {
		t.Fatalf("delegation lost actual evidence: %+v", results)
	}
	data, err := os.ReadFile(filepath.Join(dir, "child.txt"))
	if err != nil || string(data) != "genuine child output" {
		t.Fatalf("no genuine child output: %q %v", data, err)
	}
	if calls.Load() != 2 {
		t.Fatalf("unexpected provider calls: %d", calls.Load())
	}
}

func TestChildProviderDefaultsRespectModelOwnership(t *testing.T) {
	cfg := config.Default()
	cfg.Agent.DefaultModel = "second"
	cfg.Providers = []config.ProviderConfig{{Name: "first", Type: "custom", APIKey: "key", BaseURL: "http://localhost", Models: []config.ModelConfig{{ID: "first"}}}, {Name: "second", Type: "custom", APIKey: "key", BaseURL: "http://localhost", Models: []config.ModelConfig{{ID: "second"}}}}
	provider, model, err := resolveChildProvider(cfg, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if provider.Name() != "second" || model != "second" {
		t.Fatalf("default model sent to wrong provider: %s %s", provider.Name(), model)
	}
	provider, model, err = resolveChildProvider(cfg, "first", "")
	if err != nil {
		t.Fatal(err)
	}
	if provider.Name() != "first" || model != "first" {
		t.Fatalf("explicit provider received another provider's default model: %s %s", provider.Name(), model)
	}
}

package hotreload

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tokenizer"
)

type resultReporter chan ReloadResult

func (r resultReporter) OnReload([]string, error)           { panic("detailed reporter used as legacy reporter") }
func (r resultReporter) OnReloadResult(result ReloadResult) { r <- result }

func readOverrides(t *testing.T, content string) *config.UserOverrides {
	t.Helper()
	path := filepath.Join(t.TempDir(), "user.yaml")
	if err := os.WriteFile(path, []byte("schema_version: 1\n"+content), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadUserOverrides(path)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestReloadEffectsDistinguishAppliedAndPendingRestart(t *testing.T) {
	baseline := readOverrides(t, "basic:\n  model: before\n")
	rep := make(resultReporter, 8)
	a := &fakeAgent{model: "before"}
	w := &agentWiring{agent: a, prev: baseline, baseline: baseline, initialModel: a.Model(), reporter: rep}
	// Equivalent decodes allocate fresh scanner/permission bool pointers; a save
	// without a semantic change must not fabricate any reload effect.
	w.apply(readOverrides(t, "basic:\n  model: before\n"))
	if len(rep) != 0 {
		t.Fatal("identical settings reported changed")
	}
	w.apply(readOverrides(t, "basic:\n  model: after\n  theme: light\nadvanced:\n  scanners:\n    command:\n      enabled: true\n  persona:\n    tone: terse\n  system_prompt:\n    variables:\n      user: operator\n  mcp_servers:\n    - name: fixture\n      enabled: false\n"))
	result := <-rep
	if a.Model() != "after" || !reflect.DeepEqual(result.Applied, []string{"basic.model"}) {
		t.Fatalf("live effect: model=%s result=%+v", a.Model(), result)
	}
	want := []string{"basic.theme", "advanced.system_prompt", "advanced.scanners", "advanced.mcp_servers", "advanced.persona"}
	if !reflect.DeepEqual(result.RestartRequired, want) {
		t.Fatalf("restart=%v want %v", result.RestartRequired, want)
	}
	var restart *RestartRequiredError
	if !errors.As(result.Err, &restart) || !reflect.DeepEqual(restart.Fields, want) {
		t.Fatalf("missing typed partial outcome: %+v", result)
	}
	// Subsequent model edits retain unapplied scanner/prompt/MCP warnings.
	cfg := readOverrides(t, "basic:\n  model: third\n  theme: light\nadvanced:\n  scanners:\n    command:\n      enabled: true\n  persona:\n    tone: terse\n  system_prompt:\n    variables:\n      user: operator\n  mcp_servers:\n    - name: fixture\n      enabled: false\n")
	w.apply(cfg)
	if result = <-rep; !reflect.DeepEqual(result.RestartRequired, want) {
		t.Fatalf("lost pending restart: %+v", result)
	}
	// Reverting unapplied fields clears warnings; deleting the model override
	// restores the known wiring-time model rather than setting an empty ID.
	w.apply(readOverrides(t, ""))
	result = <-rep
	if result.Err != nil || len(result.RestartRequired) != 0 || a.Model() != "before" {
		t.Fatalf("revert: model=%s result=%+v", a.Model(), result)
	}
}

func TestSettingsDiffReportsUnhandledFieldsWithoutValues(t *testing.T) {
	baseline := readOverrides(t, "")
	next := readOverrides(t, "profile: paranoid\nbasic:\n  tools:\n    shell: false\n  vim_mode: true\n  confirm_writes: true\n  cost_cap_monthly: 9\nadvanced:\n  memory:\n    max_entries: 8\n  providers:\n    fixture:\n      api_key: sensitive-test-only\n  permissions:\n    denied_tools: [shell]\n  board:\n    max_concurrent: 2\n")
	got := settingsDiff(baseline, next)
	want := []string{"profile", "basic.tools", "basic.vim_mode", "basic.cost_cap_monthly", "basic.confirm_writes", "advanced.memory", "advanced.providers", "advanced.permissions", "advanced.board"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestLegacyReporterReceivesRestartRequiredError(t *testing.T) {
	baseline := readOverrides(t, "")
	rep := &captureReporter{}
	w := &agentWiring{agent: &fakeAgent{}, prev: baseline, baseline: baseline, reporter: rep}
	w.apply(readOverrides(t, "advanced:\n  persona:\n    tone: terse\n"))
	rep.mu.Lock()
	defer rep.mu.Unlock()
	var restart *RestartRequiredError
	if len(rep.errors) != 1 || !errors.As(rep.errors[0], &restart) || !reflect.DeepEqual(restart.Fields, []string{"advanced.persona"}) || len(restart.Applied) != 0 {
		t.Fatalf("legacy outcomes: %v", rep.errors)
	}
}

func awaitReload(t *testing.T, rep resultReporter) ReloadResult {
	t.Helper()
	select {
	case result := <-rep:
		return result
	case <-time.After(5 * time.Second):
		t.Fatal("reload did not settle")
		return ReloadResult{}
	}
}

func TestWireAgentActualRequestsRetainSubmittedModelThenReload(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(fmt.Sprint("stream=", streaming), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			type request struct {
				Model     string `json:"model"`
				MaxTokens int    `json:"max_tokens"`
				Stream    bool   `json:"stream"`
			}
			requests := make(chan request, 4)
			release := make(chan struct{})
			defer close(release)
			fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var input request
				if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
					t.Error(err)
					http.Error(w, "bad fixture request", 400)
					return
				}
				requests <- input
				if input.Model == "before" {
					select {
					case <-release:
					case <-r.Context().Done():
						return
					case <-ctx.Done():
						return
					}
				}
				if input.Stream {
					w.Header().Set("Content-Type", "text/event-stream")
					fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"answer\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
					return
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"id":"fixture","choices":[{"message":{"role":"assistant","content":"answer"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2}}`)
			}))
			defer func() { cancel(); fixture.Close() }()
			models := []providers.Model{{ID: "before", ContextWindow: 128000, DefaultMaxTokens: 4096}, {ID: "after", ContextWindow: 32000, DefaultMaxTokens: 1024}}
			provider, err := providers.NewProvider(providers.FactoryConfig{Type: "openai", APIKey: "local-fixture-only", BaseURL: fixture.URL, Models: models})
			if err != nil {
				t.Fatal(err)
			}
			a := agent.New(agent.Config{Provider: provider, Model: "before", ContextWindow: 128000, MaxTokens: 4096, TokenLimits: providers.TokenLimitsFor(models, 0), Tokenizer: tokenizer.NewEstimator()})
			defer a.Shutdown()
			file := filepath.Join(t.TempDir(), "user.yaml")
			if err := os.WriteFile(file, []byte("schema_version: 1\nbasic:\n  model: before\n"), 0600); err != nil {
				t.Fatal(err)
			}
			bus := New(Paths{})
			rep := make(resultReporter, 4)
			stop, err := WireAgent(ctx, bus, a, file, rep)
			if err != nil {
				t.Fatal(err)
			}
			defer stop()
			run := func() error {
				if !streaming {
					_, err := a.Run(ctx, "answer briefly")
					return err
				}
				events, err := a.Stream(ctx, "answer briefly")
				if err != nil {
					return err
				}
				for e := range events {
					if e.Error != nil {
						return e.Error
					}
				}
				return nil
			}
			done := make(chan error, 1)
			go func() { done <- run() }()
			select {
			case input := <-requests:
				if input.Model != "before" || input.MaxTokens != 4096 {
					t.Fatalf("first request %+v", input)
				}
			case <-ctx.Done():
				t.Fatal("no first request")
			}
			if err := os.WriteFile(file, []byte("schema_version: 1\nbasic:\n  model: after\n"), 0600); err != nil {
				t.Fatal(err)
			}
			bus.deliver(Event{Subject: SubjectConfig, Kind: EventModified, Path: file})
			result := awaitReload(t, rep)
			if result.Err != nil || !reflect.DeepEqual(result.Applied, []string{"basic.model"}) || a.Model() != "after" {
				t.Fatalf("reload %+v model=%s", result, a.Model())
			}
			select {
			case err := <-done:
				t.Fatalf("held request completed before release: %v", err)
			default:
			}
			release <- struct{}{}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal("held request did not settle")
			}
			if err := run(); err != nil {
				t.Fatal(err)
			}
			select {
			case input := <-requests:
				if input.Model != "after" || input.MaxTokens != 1024 {
					t.Fatalf("reloaded request %+v", input)
				}
			case <-ctx.Done():
				t.Fatal("no second request")
			}
			if a.BudgetReport().MaxTokens != 32000 {
				t.Fatalf("model capacity not refreshed: %+v", a.BudgetReport())
			}
			// Invalid reloads preserve the last good model/snapshot; correcting the
			// file to the same model produces no fabricated successful effect.
			if err := os.WriteFile(file, []byte("basic: [broken"), 0600); err != nil {
				t.Fatal(err)
			}
			bus.deliver(Event{Subject: SubjectConfig, Kind: EventModified, Path: file})
			if result := awaitReload(t, rep); result.Err == nil || len(result.Applied) != 0 || a.Model() != "after" {
				t.Fatalf("invalid reload %+v", result)
			}
			if err := os.Remove(file); err != nil {
				t.Fatal(err)
			}
			bus.deliver(Event{Subject: SubjectConfig, Kind: EventRemoved, Path: file})
			if result := awaitReload(t, rep); result.Err != nil || !reflect.DeepEqual(result.Applied, []string{"basic.model"}) || a.Model() != "before" {
				t.Fatalf("removed override: %+v model=%s", result, a.Model())
			}
		})
	}
}

func TestUnknownModelFallbackRemainsUnappliedUntilExplicitModel(t *testing.T) {
	baseline := readOverrides(t, "")
	rep := make(resultReporter, 8)
	a := &fakeAgent{}
	w := &agentWiring{agent: a, prev: baseline, baseline: baseline, reporter: rep}
	w.apply(readOverrides(t, "basic:\n  model: selected\n"))
	if result := <-rep; result.Err != nil || a.Model() != "selected" {
		t.Fatalf("selection %+v", result)
	}
	w.apply(readOverrides(t, ""))
	if result := <-rep; !reflect.DeepEqual(result.RestartRequired, []string{"basic.model"}) || len(result.Applied) != 0 || a.Model() != "selected" {
		t.Fatalf("unknown fallback %+v model=%s", result, a.Model())
	}
	w.apply(readOverrides(t, "basic:\n  theme: light\n"))
	if result := <-rep; !reflect.DeepEqual(result.RestartRequired, []string{"basic.theme", "basic.model"}) {
		t.Fatalf("pending reset lost %+v", result)
	}
	w.apply(readOverrides(t, "basic:\n  model: restored\n"))
	if result := <-rep; result.Err != nil || a.Model() != "restored" {
		t.Fatalf("explicit model %+v", result)
	}
}

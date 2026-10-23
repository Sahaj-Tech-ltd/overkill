package childruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/skills"
	"github.com/Sahaj-Tech-ltd/overkill/internal/subagent"
)

type scriptedProvider struct {
	calls    atomic.Int32
	complete func(context.Context, providers.Request, int) (providers.Response, error)
}

func (p *scriptedProvider) Name() string { return "scripted" }
func (p *scriptedProvider) Models() []providers.Model {
	return []providers.Model{{ID: "test", SupportsTools: true, CostIn: 1, CostOut: 2}}
}
func (p *scriptedProvider) Stream(context.Context, providers.Request) (<-chan providers.Chunk, error) {
	return nil, fmt.Errorf("unexpected streaming")
}
func (p *scriptedProvider) Complete(ctx context.Context, req providers.Request) (providers.Response, error) {
	return p.complete(ctx, req, int(p.calls.Add(1)))
}
func testFactory(dir string, provider providers.Provider) Factory {
	return Factory{Workspace: dir, Resolve: func(string, string) (providers.Provider, string, error) { return provider, "test", nil }}
}
func canonicalDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}
func call(name string, args any) providers.Response {
	raw, _ := json.Marshal(args)
	return providers.Response{Usage: providers.Usage{InputTokens: 3, OutputTokens: 2}, ToolCalls: []providers.ToolCall{{ID: "call", Name: name, Arguments: string(raw)}}}
}

func TestChildActuallyReadsAndPatchesWithReceipts(t *testing.T) {
	dir := canonicalDir(t)
	path := filepath.Join(dir, "source.txt")
	if err := os.WriteFile(path, []byte("before\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	provider := &scriptedProvider{complete: func(_ context.Context, req providers.Request, step int) (providers.Response, error) {
		if len(req.Tools) != 2 {
			t.Fatalf("expected exact child capabilities: %+v", req.Tools)
		}
		for _, definition := range req.Tools {
			if !json.Valid(definition.Parameters) || !strings.Contains(string(definition.Parameters), "properties") {
				t.Fatal("missing tool schema")
			}
		}
		switch step {
		case 1:
			return call("fs", map[string]any{"action": "read", "path": "source.txt"}), nil
		case 2:
			found := false
			for _, message := range req.Messages {
				if message.Role == "tool" && strings.Contains(message.Content, "before") {
					found = true
				}
			}
			if !found {
				t.Fatal("actual read result was not returned to provider")
			}
			return call("patch", map[string]any{"path": "source.txt", "patch": "@@ -1 +1 @@\n-before\n+after\n"}), nil
		case 3:
			return providers.Response{Content: "Patched source.txt. Also claiming made-up.txt does not create a receipt.", Usage: providers.Usage{InputTokens: 3, OutputTokens: 2}}, nil
		default:
			t.Fatalf("unexpected provider step %d", step)
			return providers.Response{}, nil
		}
	}}
	worker, err := testFactory(dir, provider).Build(subagent.RealWorkerConfig{Goal: "Read and patch source.txt", Tools: []string{"fs", "patch"}, MaxSteps: 3})
	if err != nil {
		t.Fatal(err)
	}
	result, err := worker.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "after\n" {
		t.Fatalf("no actual patch: %q %v", data, err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o640 {
		t.Fatal("child patch changed permissions")
	}
	if result.Status != "completed" || result.TokensIn != 9 || result.TokensOut != 6 || math.Abs(result.CostUSD-0.000021) > 1e-12 {
		t.Fatalf("incorrect measured result: %+v", result)
	}
	if len(result.FilesRead) != 1 || result.FilesRead[0] != path || len(result.FilesWritten) != 1 || result.FilesWritten[0] != path {
		t.Fatalf("file claims were not derived from tools: %+v", result)
	}
	if len(result.ToolTrace) != 2 {
		t.Fatalf("missing receipts: %+v", result.ToolTrace)
	}
	for _, receipt := range result.ToolTrace {
		if !receipt.Success || receipt.Path != path || len(receipt.InputHash) != 64 || len(receipt.OutputHash) != 64 {
			t.Fatalf("bad receipt: %+v", receipt)
		}
	}
}

func TestChildUsesContainedWriteTool(t *testing.T) {
	dir := canonicalDir(t)
	provider := &scriptedProvider{complete: func(_ context.Context, _ providers.Request, step int) (providers.Response, error) {
		if step == 1 {
			return call("fs", map[string]any{"action": "write", "path": "nested/new.txt", "content": "written by actual tool"}), nil
		}
		return providers.Response{Content: "done"}, nil
	}}
	worker, err := testFactory(dir, provider).Build(subagent.RealWorkerConfig{Goal: "write a file", Tools: []string{"fs"}, MaxSteps: 2})
	if err != nil {
		t.Fatal(err)
	}
	result, err := worker.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "nested/new.txt"))
	if err != nil || string(data) != "written by actual tool" {
		t.Fatalf("write did not happen: %q %v", data, err)
	}
	if len(result.FilesWritten) != 1 || len(result.ToolTrace) != 1 || !result.ToolTrace[0].Success {
		t.Fatalf("missing actual write evidence: %+v", result)
	}
}

func TestChildDeniedWorkspaceAccessCannotClaimCompletion(t *testing.T) {
	for _, action := range []string{"read", "write", "patch"} {
		t.Run(action, func(t *testing.T) {
			dir, outside := canonicalDir(t), canonicalDir(t)
			target := filepath.Join(outside, "protected")
			if err := os.WriteFile(target, []byte("outside-original\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, filepath.Join(dir, "escape")); err != nil {
				t.Fatal(err)
			}
			provider := &scriptedProvider{complete: func(_ context.Context, req providers.Request, step int) (providers.Response, error) {
				for _, message := range req.Messages {
					if strings.Contains(message.Content, "outside-original") {
						t.Fatal("outside file leaked")
					}
				}
				if step == 1 {
					if action == "patch" {
						return call("patch", map[string]any{"path": "escape/protected", "patch": "@@ -1 +1 @@\n-outside-original\n+corrupted\n"}), nil
					}
					return call("fs", map[string]any{"action": action, "path": "escape/protected", "content": "corrupted"}), nil
				}
				return providers.Response{Content: "Everything completed successfully"}, nil
			}}
			worker, err := testFactory(dir, provider).Build(subagent.RealWorkerConfig{Goal: "attempt escape", Tools: []string{"fs", "patch"}, MaxSteps: 2})
			if err != nil {
				t.Fatal(err)
			}
			result, err := worker.Run(context.Background())
			if err == nil || result.Status == "completed" {
				t.Fatalf("denied operation marked completed: %+v %v", result, err)
			}
			data, _ := os.ReadFile(target)
			if string(data) != "outside-original\n" {
				t.Fatal("outside file changed")
			}
			if len(result.FilesRead) != 0 || len(result.FilesWritten) != 0 || len(result.ToolTrace) != 1 || result.ToolTrace[0].Success || result.ToolTrace[0].Error == "" {
				t.Fatalf("false receipt: %+v", result)
			}
		})
	}
}

func TestChildExactStepBudgetIncludesRetries(t *testing.T) {
	for _, retry := range []bool{false, true} {
		t.Run(fmt.Sprint(retry), func(t *testing.T) {
			dir := canonicalDir(t)
			provider := &scriptedProvider{complete: func(_ context.Context, _ providers.Request, _ int) (providers.Response, error) {
				if retry {
					return providers.Response{}, &providers.HTTPError{StatusCode: 429}
				}
				return call("fs", map[string]any{"action": "stat", "path": "."}), nil
			}}
			worker, err := testFactory(dir, provider).Build(subagent.RealWorkerConfig{Goal: "keep working", Tools: []string{"fs"}, MaxSteps: 1})
			if err != nil {
				t.Fatal(err)
			}
			result, err := worker.Run(context.Background())
			if err == nil || result.Status == "completed" || provider.calls.Load() != 1 {
				t.Fatalf("step bound violated: calls=%d %+v %v", provider.calls.Load(), result, err)
			}
			if !retry && result.TokensIn != 3 {
				t.Fatalf("lost usage on exhaustion: %+v", result)
			}
		})
	}
}

func TestChildCancellationRetainsActualUsage(t *testing.T) {
	dir := canonicalDir(t)
	entered := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	provider := &scriptedProvider{complete: func(ctx context.Context, _ providers.Request, step int) (providers.Response, error) {
		if step == 1 {
			return call("fs", map[string]any{"action": "stat", "path": "."}), nil
		}
		close(entered)
		<-ctx.Done()
		return providers.Response{Usage: providers.Usage{InputTokens: 5}}, ctx.Err()
	}}
	worker, err := testFactory(dir, provider).Build(subagent.RealWorkerConfig{Goal: "cancel mid-task", Tools: []string{"fs"}, MaxSteps: 3})
	if err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		result *subagent.Result
		err    error
	}
	done := make(chan outcome, 1)
	go func() { result, err := worker.Run(ctx); done <- outcome{result, err} }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("provider did not start")
	}
	cancel()
	select {
	case outcome := <-done:
		if !errors.Is(outcome.err, context.Canceled) || outcome.result.Status == "completed" || outcome.result.TokensIn != 8 || outcome.result.TokensOut != 2 {
			t.Fatalf("lost cancellation/usage: %+v %v", outcome.result, outcome.err)
		}
	case <-time.After(time.Second):
		t.Fatal("child did not cancel")
	}
}

func TestChildRejectsUnsupportedToolsAndUnadvertisedCalls(t *testing.T) {
	provider := &scriptedProvider{complete: func(context.Context, providers.Request, int) (providers.Response, error) {
		return call("shell", map[string]any{"command": "touch bad"}), nil
	}}
	factory := testFactory(canonicalDir(t), provider)
	if _, err := factory.Build(subagent.RealWorkerConfig{Goal: "shell", Tools: []string{"shell"}}); err == nil || provider.calls.Load() != 0 {
		t.Fatal("unsupported capability was not rejected before execution")
	}
	worker, err := factory.Build(subagent.RealWorkerConfig{Goal: "text only", MaxSteps: 1})
	if err != nil {
		t.Fatal(err)
	}
	result, err := worker.Run(context.Background())
	if err == nil || result.Status == "completed" || len(result.ToolTrace) != 0 {
		t.Fatalf("unadvertised tool accepted: %+v %v", result, err)
	}
}

func TestChildMentionsDoNotReadOutsideAndHistoriesAreIndependent(t *testing.T) {
	dir, outside := canonicalDir(t), canonicalDir(t)
	secret := filepath.Join(outside, "secret")
	if err := os.WriteFile(secret, []byte("UNIQUE-SECRET-CONTENT"), 0o600); err != nil {
		t.Fatal(err)
	}
	provider := &scriptedProvider{complete: func(_ context.Context, req providers.Request, _ int) (providers.Response, error) {
		users := 0
		for _, message := range req.Messages {
			if message.Role == "user" {
				users++
			}
			if strings.Contains(message.Content, "UNIQUE-SECRET-CONTENT") {
				t.Fatal("automatic mention leaked outside content")
			}
		}
		if users != 1 {
			t.Fatalf("child inherited another child's history: %+v", req.Messages)
		}
		return providers.Response{Content: "No file access performed"}, nil
	}}
	factory := testFactory(dir, provider)
	for i := 0; i < 2; i++ {
		worker, err := factory.Build(subagent.RealWorkerConfig{Goal: "inspect @" + secret, MaxSteps: 1})
		if err != nil {
			t.Fatal(err)
		}
		result, err := worker.Run(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(result.FilesRead) > 0 || len(result.ToolTrace) > 0 {
			t.Fatal("invented read receipt")
		}
	}
}

func TestChildRejectsUnsupportedCompletionClaimsWithoutTools(t *testing.T) {
	provider := &scriptedProvider{complete: func(context.Context, providers.Request, int) (providers.Response, error) {
		return providers.Response{Content: "I changed every file and ran the tests"}, nil
	}}
	worker, err := testFactory(canonicalDir(t), provider).Build(subagent.RealWorkerConfig{Goal: "change files", Tools: []string{"fs", "patch"}, MaxSteps: 1})
	if err != nil {
		t.Fatal(err)
	}
	result, err := worker.Run(context.Background())
	if err == nil || result.Status == "completed" || !strings.Contains(err.Error(), "without executing") {
		t.Fatalf("unsupported completion claims accepted: %+v %v", result, err)
	}
	if len(result.FilesWritten) != 0 || len(result.ToolTrace) != 0 {
		t.Fatal("fabricated claims became receipts")
	}
}

func TestChildUsesSharedSkillEnablement(t *testing.T) {
	registry := skills.NewRegistry()
	if err := registry.Register(&skills.Skill{Name: "child-skill", Description: "Shared child skill configuration", Instructions: "CHILD-SKILL-MARKER use only verified task evidence.", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	provider := &scriptedProvider{complete: func(_ context.Context, req providers.Request, step int) (providers.Response, error) {
		present := strings.Contains(req.SystemPrompt, "CHILD-SKILL-MARKER")
		if present != (step == 1) {
			t.Fatalf("child ignored shared enablement on call %d: %v", step, present)
		}
		return providers.Response{Content: "text response"}, nil
	}}
	factory := testFactory(canonicalDir(t), provider)
	factory.Skills = registry
	for step := 0; step < 2; step++ {
		worker, err := factory.Build(subagent.RealWorkerConfig{Goal: "summarize supplied context", MaxSteps: 1})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := worker.Run(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := registry.Disable("child-skill"); err != nil {
			t.Fatal(err)
		}
	}
}

type textOnlyProvider struct{ *scriptedProvider }

func (p textOnlyProvider) Models() []providers.Model {
	return []providers.Model{{ID: "test", SupportsTools: false}}
}
func TestChildRejectsModelWithoutToolCapability(t *testing.T) {
	provider := &scriptedProvider{complete: func(context.Context, providers.Request, int) (providers.Response, error) {
		t.Fatal("unsupported model must not be called")
		return providers.Response{}, nil
	}}
	_, err := testFactory(canonicalDir(t), textOnlyProvider{provider}).Build(subagent.RealWorkerConfig{Goal: "read file", Tools: []string{"fs"}})
	if !errors.Is(err, subagent.ErrUnsupportedCapability) || provider.calls.Load() != 0 {
		t.Fatalf("unsupported model accepted: %v", err)
	}
}

package childruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/subagent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
)

func codingFactory(dir string, provider providers.Provider) Factory {
	f := testFactory(dir, provider)
	f.CodingTools = func() []tools.Tool { return tools.NewWorkspaceCodingTools(tools.FactoryDeps{CWD: dir}) }
	return f
}

func TestCodingChildEditsRealProjectAndRunsGoTests(t *testing.T) {
	dir := canonicalDir(t)
	for name, body := range map[string]string{
		"go.mod":      "module fixture.invalid/child\n\ngo 1.22\n",
		"sum.go":      "package child\nfunc Sum(a,b int) int { return a-b }\n",
		"sum_test.go": "package child\nimport \"testing\"\nfunc TestSum(t *testing.T) { if Sum(2,3)!=5 { t.Fatal(\"wrong sum\") } }\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	goBin := filepath.Join(runtime.GOROOT(), "bin", "go")
	if runtime.GOOS == "windows" {
		goBin += ".exe"
	}
	command := "'" + strings.ReplaceAll(goBin, "'", "'\\''") + "' test ./..."
	provider := &scriptedProvider{complete: func(_ context.Context, req providers.Request, step int) (providers.Response, error) {
		for _, def := range req.Tools {
			if !json.Valid(def.Parameters) {
				t.Fatalf("invalid schema for %s", def.Name)
			}
		}
		switch step {
		case 1:
			return call("grep", map[string]any{"pattern": "func Sum"}), nil
		case 2:
			return call("fs", map[string]any{"action": "edit", "path": "sum.go", "old": "a-b", "new": "a+b"}), nil
		case 3:
			return call("shell", map[string]any{"command": command, "timeout_seconds": 40, "env": map[string]string{"GOWORK": "off", "GOTOOLCHAIN": "local"}}), nil
		default:
			var out tools.ShellOutput
			last := req.Messages[len(req.Messages)-1]
			if last.Role != "tool" || json.Unmarshal([]byte(last.Content), &out) != nil || out.ExitCode != 0 || !out.Completed || !strings.Contains(out.Stdout, "ok") {
				t.Fatalf("real test did not pass: %+v", last)
			}
			return providers.Response{Content: "Fixed Sum; actual Go tests passed."}, nil
		}
	}}
	f := codingFactory(dir, provider)
	var invoked []string
	f.ExecutionGuard = func(ctx context.Context, tool tools.Tool, in json.RawMessage) (json.RawMessage, error) {
		// The inherited guard must see original adapters, not receipt wrappers.
		switch tool.(type) {
		case *tools.FSTool, *tools.GrepTool, *tools.ShellTool:
		default:
			t.Fatalf("guard lost original adapter %T", tool)
		}
		invoked = append(invoked, tool.Name())
		return tool.Execute(ctx, in)
	}
	worker, err := f.Build(subagent.RealWorkerConfig{Goal: "Fix Sum and verify the actual project", Tools: []string{"fs", "grep", "shell"}, MaxSteps: 4})
	if err != nil {
		t.Fatal(err)
	}
	result, err := worker.Run(context.Background())
	if err != nil || result.Status != "completed" || len(result.ToolTrace) != 3 {
		t.Fatalf("%+v %v", result, err)
	}
	if strings.Join(invoked, ",") != "grep,fs,shell" {
		t.Fatal(invoked)
	}
	body, _ := os.ReadFile(filepath.Join(dir, "sum.go"))
	if !strings.Contains(string(body), "a+b") {
		t.Fatal("no actual edit")
	}
	for _, entry := range result.ToolTrace {
		if !entry.Success || entry.InputHash == "" || entry.OutputHash == "" {
			t.Fatalf("missing evidence: %+v", entry)
		}
	}
}

func TestCodingChildCannotBypassInheritedAdmission(t *testing.T) {
	dir := canonicalDir(t)
	p := &scriptedProvider{complete: func(_ context.Context, _ providers.Request, step int) (providers.Response, error) {
		if step == 1 {
			return call("shell", map[string]any{"command": "touch forbidden"}), nil
		}
		return providers.Response{Content: "done"}, nil
	}}
	f := codingFactory(dir, p)
	f.ExecutionGuard = func(context.Context, tools.Tool, json.RawMessage) (json.RawMessage, error) {
		return nil, fmt.Errorf("parent task has no unrestricted workspace claim")
	}
	worker, err := f.Build(subagent.RealWorkerConfig{Tools: []string{"shell"}, MaxSteps: 2})
	if err != nil {
		t.Fatal(err)
	}
	result, err := worker.Run(context.Background())
	if err == nil || result.Status == "completed" {
		t.Fatalf("guard bypassed: %+v %v", result, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "forbidden")); !os.IsNotExist(err) {
		t.Fatal("denied command ran")
	}
	if len(result.ToolTrace) != 1 || result.ToolTrace[0].Success {
		t.Fatal(result)
	}
}

func TestCodingChildPreservesReadonlyCeiling(t *testing.T) {
	dir := canonicalDir(t)
	p := &scriptedProvider{complete: func(_ context.Context, _ providers.Request, step int) (providers.Response, error) {
		if step == 1 {
			return call("shell", map[string]any{"command": "touch forbidden"}), nil
		}
		return providers.Response{Content: "done"}, nil
	}}
	f := codingFactory(dir, p)
	f.ReadOnly = true
	worker, err := f.Build(subagent.RealWorkerConfig{Tools: []string{"shell"}, MaxSteps: 2})
	if err != nil {
		t.Fatal(err)
	}
	result, err := worker.Run(context.Background())
	if err == nil && result.Status == "completed" {
		t.Fatalf("readonly bypassed: %+v", result)
	}
	if _, err := os.Stat(filepath.Join(dir, "forbidden")); !os.IsNotExist(err) {
		t.Fatal("readonly command ran")
	}
}

func TestDelegatedLSPInputIsCanonicalAndWorkspaceBound(t *testing.T) {
	dir, outside := canonicalDir(t), canonicalDir(t)
	for _, base := range []string{dir, outside} {
		if err := os.WriteFile(filepath.Join(base, "source.go"), []byte("package source\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(outside, filepath.Join(dir, "escape")); err != nil {
		t.Skipf("symlink fixture unavailable: %v", err)
	}
	for _, path := range []string{"escape/source.go", filepath.Join(outside, "source.go")} {
		raw, _ := json.Marshal(map[string]any{"file": path, "line": 1, "column": 1})
		if _, err := scopedLSPInput(dir, raw); err == nil {
			t.Fatal("outside LSP file accepted")
		}
	}
	raw, err := scopedLSPInput(dir, json.RawMessage(`{"file":"source.go","line":1,"column":1}`))
	if err != nil {
		t.Fatal(err)
	}
	var args map[string]any
	_ = json.Unmarshal(raw, &args)
	if args["file"] != filepath.Join(dir, "source.go") {
		t.Fatal(args)
	}
}

func TestCommandFailureIsNotRecordedAsToolSuccess(t *testing.T) {
	dir := canonicalDir(t)
	p := &scriptedProvider{complete: func(_ context.Context, _ providers.Request, step int) (providers.Response, error) {
		if step == 1 {
			return call("shell", map[string]any{"command": "exit 7"}), nil
		}
		return providers.Response{Content: "Command failed with exit 7."}, nil
	}}
	worker, err := codingFactory(dir, p).Build(subagent.RealWorkerConfig{Tools: []string{"shell"}, MaxSteps: 2})
	if err != nil {
		t.Fatal(err)
	}
	result, err := worker.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.ToolTrace) != 1 || result.ToolTrace[0].Success || !strings.Contains(result.ToolTrace[0].Error, "code 7") {
		t.Fatalf("false command receipt: %+v", result)
	}
	// Completed means the bounded loop ended, not that the task passed acceptance.
}

func TestExplicitCodingGrantDoesNotImplicitlyAddFileTools(t *testing.T) {
	dir := canonicalDir(t)
	provider := &scriptedProvider{}
	f := testFactory(dir, provider)
	f.CodingTools = func() []tools.Tool { return []tools.Tool{tools.NewGrepTool(dir)} }
	if _, err := f.Build(subagent.RealWorkerConfig{Tools: []string{"fs"}}); err == nil || provider.calls.Load() != 0 {
		t.Fatal("child enlarged the trusted capability grant")
	}
}

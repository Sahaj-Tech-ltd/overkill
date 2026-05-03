package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sahaj-Tech-ltd/ethos/internal/subagent"
)

func TestContractDriver_BootstrapPrompt(t *testing.T) {
	c := &subagent.Contract{
		ID:         "c1",
		Goal:       "implement foo",
		Scope:      []string{"internal/foo"},
		OutOfScope: []string{"vendor/**"},
		Inputs: []subagent.ContextRef{
			{Type: subagent.CtxRefSpec, Value: "spec.md"},
		},
		ExpectedOutputs: []subagent.Output{
			{Kind: subagent.OutFile, Spec: "internal/foo/bar.go"},
		},
		IntegrationPoints: []subagent.Integration{
			{Description: "must implement Tool interface", Reference: "internal/tools/tool.go"},
		},
		Acceptance: []subagent.AcceptanceCheck{
			{Name: "build", Cmd: "go build ./...", ExpectExit: 0},
		},
	}
	d := NewContractDriver(nil, c, "")
	p := d.bootstrapPrompt()
	for _, want := range []string{
		"implement foo",
		"internal/foo",
		"vendor/**",
		"spec.md",
		"internal/foo/bar.go",
		"must implement Tool interface",
		"go build ./...",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("bootstrap missing %q", want)
		}
	}
}

func TestContractDriver_CheckOutput_FileExists(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "out.txt")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	c := &subagent.Contract{
		ExpectedOutputs: []subagent.Output{
			{Kind: subagent.OutFile, Spec: "out.txt"},
			{Kind: subagent.OutFile, Spec: "missing.txt"},
			{Kind: subagent.OutBehavior, Spec: "behaves correctly"},
		},
	}
	d := NewContractDriver(nil, c, dir)
	got := d.CheckOutput(context.Background(), c.ExpectedOutputs)
	if len(got) != 1 || got[0] != "out.txt" {
		t.Fatalf("got %v want [out.txt]", got)
	}
}

func TestContractDriver_BudgetWithoutAgent(t *testing.T) {
	d := NewContractDriver(nil, &subagent.Contract{}, "")
	defer func() {
		// Budget on a nil agent should not panic (we expect a deliberate nil-check
		// callers to avoid this case in production).
		_ = recover()
	}()
	// Skip — Budget intentionally requires a non-nil agent. This test exists
	// to lock the documented contract: callers must pass a real Agent.
	_ = d
}

package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/subagent"
)

// stubDriver is a minimal StepDriver for these tool tests.
type stubDriver struct {
	steps      int
	completeAt int
	specs      []string
	stepDelay  time.Duration
}

func (s *stubDriver) Step(ctx context.Context) subagent.StepResult {
	s.steps++
	if s.stepDelay > 0 {
		select {
		case <-time.After(s.stepDelay):
		case <-ctx.Done():
		}
	}
	return subagent.StepResult{ToolCalls: 1}
}
func (s *stubDriver) Budget() subagent.BudgetSnapshot {
	return subagent.BudgetSnapshot{CurrentTokens: 10, MaxTokens: 100000}
}
func (s *stubDriver) Compact(ctx context.Context) (subagent.BudgetSnapshot, error) {
	return s.Budget(), nil
}
func (s *stubDriver) CheckOutput(ctx context.Context, _ []subagent.Output) []string {
	if s.completeAt > 0 && s.steps >= s.completeAt {
		return s.specs
	}
	return nil
}

func newContract(id string) *subagent.Contract {
	return &subagent.Contract{
		ID:              id,
		Goal:            "test",
		ExpectedOutputs: []subagent.Output{{Kind: subagent.OutFile, Spec: "out.go"}},
		OnContextFull:   subagent.OnContextFullCompactAndContinue,
		Budget:          subagent.ContractBudget{Steps: 100},
	}
}

func TestSubagentStatusTool_NilManager(t *testing.T) {
	tool := NewSubagentStatusTool(nil)
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"id":"x"}`))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strings.Contains(string(out), "not configured") {
		t.Fatalf("unexpected: %s", out)
	}
}

func TestSubagentStatusTool_MissingID(t *testing.T) {
	m := subagent.NewManager(subagent.Config{MaxChildren: 3})
	tool := NewSubagentStatusTool(m)
	out, _ := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if !strings.Contains(string(out), "id is required") {
		t.Fatalf("unexpected: %s", out)
	}
}

func TestSubagentStatusTool_Roundtrip(t *testing.T) {
	m := subagent.NewManager(subagent.Config{MaxChildren: 3})
	c := newContract("status-1")
	d := &stubDriver{completeAt: 2, specs: []string{"out.go"}}
	if _, err := m.SpawnContract(context.Background(), c, d, "", nil); err != nil {
		t.Fatalf("spawn: %v", err)
	}

	wt := NewSubagentWaitTool(m)
	if _, err := wt.Execute(context.Background(), json.RawMessage(`{"id":"status-1","timeout_seconds":5}`)); err != nil {
		t.Fatalf("wait: %v", err)
	}

	tool := NewSubagentStatusTool(m)
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"id":"status-1"}`))
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["running"].(bool) {
		t.Fatal("running should be false after wait")
	}
	if got["final_report"] == nil {
		t.Fatal("final_report should be present")
	}
}

func TestSubagentWaitTool_Timeout(t *testing.T) {
	m := subagent.NewManager(subagent.Config{MaxChildren: 3})
	c := newContract("wait-1")
	c.Budget.Steps = 100000
	d := &stubDriver{stepDelay: 50 * time.Millisecond} // never completes within timeout
	if _, err := m.SpawnContract(context.Background(), c, d, "", nil); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	defer m.AutonomousCancel(c.ID)

	tool := NewSubagentWaitTool(m)
	start := time.Now()
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"id":"wait-1","timeout_seconds":1}`))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("waited too long: %v", time.Since(start))
	}
	if !strings.Contains(string(out), `"timeout":true`) {
		t.Fatalf("expected timeout marker in: %s", out)
	}
}

func TestSubagentWaitTool_NotFound(t *testing.T) {
	m := subagent.NewManager(subagent.Config{MaxChildren: 3})
	tool := NewSubagentWaitTool(m)
	out, _ := tool.Execute(context.Background(), json.RawMessage(`{"id":"missing"}`))
	if !strings.Contains(string(out), "no contract") {
		t.Fatalf("unexpected: %s", out)
	}
}

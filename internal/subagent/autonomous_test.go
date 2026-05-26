package subagent

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// fakeDriver implements StepDriver for autonomous-runner tests.
type fakeDriver struct {
	steps        atomic.Int32
	current      atomic.Int32 // current tokens
	max          int
	completeAt   int    // step number at which CheckOutput returns all specs
	failAt       int    // step number at which Step returns an error
	failErr      error
	doneAt       int    // step number at which Step.Done = true
	compactDelta int    // tokens to subtract on Compact
	compactErr   error
	specs        []string
}

func (f *fakeDriver) Step(ctx context.Context) StepResult {
	n := int(f.steps.Add(1))
	if f.failAt > 0 && n == f.failAt {
		return StepResult{Err: f.failErr}
	}
	// Simulate token growth.
	f.current.Add(50)
	r := StepResult{ToolCalls: 1, Tokens: int(f.current.Load()), Output: "step output"}
	if f.doneAt > 0 && n >= f.doneAt {
		r.Done = true
	}
	return r
}

func (f *fakeDriver) Budget() BudgetSnapshot {
	return BudgetSnapshot{CurrentTokens: int(f.current.Load()), MaxTokens: f.max}
}

func (f *fakeDriver) Compact(ctx context.Context) (BudgetSnapshot, error) {
	if f.compactErr != nil {
		return f.Budget(), f.compactErr
	}
	cur := f.current.Load() - int32(f.compactDelta)
	if cur < 0 {
		cur = 0
	}
	f.current.Store(cur)
	return f.Budget(), nil
}

func (f *fakeDriver) CheckOutput(ctx context.Context, outputs []Output) []string {
	if f.completeAt > 0 && int(f.steps.Load()) >= f.completeAt {
		return f.specs
	}
	return nil
}

func runnerContract(specs []string) *Contract {
	outs := make([]Output, len(specs))
	for i, s := range specs {
		outs[i] = Output{Kind: OutFile, Spec: s}
	}
	return &Contract{
		ID:              "auto-1",
		Goal:            "test",
		ExpectedOutputs: outs,
		OnContextFull:   OnContextFullCompactAndContinue,
		Budget:          ContractBudget{Steps: 100},
	}
}

func TestAutonomous_CompletesWhenAllOutputsSatisfied(t *testing.T) {
	d := &fakeDriver{max: 10000, completeAt: 3, specs: []string{"a", "b"}}
	c := runnerContract([]string{"a", "b"})
	r, err := NewAutonomousRunner(AutonomousConfig{Contract: c, Driver: d})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	rep, err := r.Run(context.Background())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if rep.Status != "completed" {
		t.Fatalf("status = %q want completed (reason=%s)", rep.Status, rep.Reason)
	}
	for _, o := range rep.Outputs {
		if !o.Satisfied {
			t.Errorf("output %s not satisfied", o.Spec)
		}
	}
}

func TestAutonomous_AutoCompactWhenContextFull(t *testing.T) {
	d := &fakeDriver{max: 100, compactDelta: 90, completeAt: 100, specs: []string{"a"}}
	d.current.Store(85) // already at 85% on first iteration
	c := runnerContract([]string{"a"})
	c.Budget.Steps = 5
	r, _ := NewAutonomousRunner(AutonomousConfig{Contract: c, Driver: d})
	rep, _ := r.Run(context.Background())
	if rep.Status == "completed" {
		t.Fatal("did not expect completion before completeAt step")
	}
	// Verify a compaction must have occurred — current would be < 50 only if compact ran.
	if d.current.Load() > 200 {
		// after 5 steps each adds 50 → 85+250=335, with one compaction subtracting 90 → 245-ish.
		// loose bound: ensure compaction actually fired
		t.Fatalf("compact does not appear to have run, tokens=%d", d.current.Load())
	}
}

func TestAutonomous_ContextFullPolicyFail(t *testing.T) {
	d := &fakeDriver{max: 100}
	d.current.Store(95)
	c := runnerContract([]string{"a"})
	c.OnContextFull = OnContextFullFail
	r, _ := NewAutonomousRunner(AutonomousConfig{Contract: c, Driver: d})
	rep, _ := r.Run(context.Background())
	if rep.Status != "exhausted" {
		t.Fatalf("status = %q want exhausted", rep.Status)
	}
}

func TestAutonomous_ContextFullPolicyHandoff(t *testing.T) {
	d := &fakeDriver{max: 100}
	d.current.Store(95)
	c := runnerContract([]string{"a"})
	c.OnContextFull = OnContextFullHandoffToParent
	r, _ := NewAutonomousRunner(AutonomousConfig{Contract: c, Driver: d})
	rep, _ := r.Run(context.Background())
	if rep.Status != "handed_off" {
		t.Fatalf("status = %q want handed_off (reason=%s)", rep.Status, rep.Reason)
	}
}

func TestAutonomous_CompactInsufficientHandoffs(t *testing.T) {
	d := &fakeDriver{max: 100, compactDelta: 1} // compaction barely helps
	d.current.Store(95)
	c := runnerContract([]string{"a"})
	r, _ := NewAutonomousRunner(AutonomousConfig{Contract: c, Driver: d})
	rep, _ := r.Run(context.Background())
	if rep.Status != "handed_off" {
		t.Fatalf("status = %q want handed_off", rep.Status)
	}
}

func TestAutonomous_StepBudgetExhausted(t *testing.T) {
	d := &fakeDriver{max: 100000}
	c := runnerContract([]string{"never-satisfied"})
	c.Budget.Steps = 3
	r, _ := NewAutonomousRunner(AutonomousConfig{Contract: c, Driver: d})
	rep, _ := r.Run(context.Background())
	if rep.Status != "exhausted" {
		t.Fatalf("status = %q want exhausted", rep.Status)
	}
	if rep.StepsUsed < 3 {
		t.Fatalf("steps = %d want >= 3", rep.StepsUsed)
	}
}

func TestAutonomous_ContractViolationStopsRunner(t *testing.T) {
	d := &fakeDriver{max: 100000, failAt: 2, failErr: ContractViolation{Criterion: "scope", Reason: "outside"}}
	c := runnerContract([]string{"a"})
	r, _ := NewAutonomousRunner(AutonomousConfig{Contract: c, Driver: d})
	rep, _ := r.Run(context.Background())
	if rep.Status != "violated" {
		t.Fatalf("status = %q want violated", rep.Status)
	}
	if len(rep.Violations) == 0 {
		t.Fatal("expected at least one violation")
	}
}

func TestAutonomous_DoneWithoutAllOutputsIsViolation(t *testing.T) {
	d := &fakeDriver{max: 100000, doneAt: 1}
	c := runnerContract([]string{"never-satisfied"})
	r, _ := NewAutonomousRunner(AutonomousConfig{Contract: c, Driver: d})
	rep, _ := r.Run(context.Background())
	if rep.Status != "violated" {
		t.Fatalf("status = %q want violated (reason=%s)", rep.Status, rep.Reason)
	}
}

func TestAutonomous_GenericErrorFails(t *testing.T) {
	d := &fakeDriver{max: 100000, failAt: 1, failErr: errors.New("boom")}
	c := runnerContract([]string{"a"})
	r, _ := NewAutonomousRunner(AutonomousConfig{Contract: c, Driver: d})
	rep, _ := r.Run(context.Background())
	if rep.Status != "failed" {
		t.Fatalf("status = %q want failed", rep.Status)
	}
}

func TestAutonomous_CancelStops(t *testing.T) {
	d := &fakeDriver{max: 100000}
	c := runnerContract([]string{"never-satisfied"})
	c.Budget.Steps = 1000
	r, _ := NewAutonomousRunner(AutonomousConfig{Contract: c, Driver: d})
	r.Cancel()
	rep, _ := r.Run(context.Background())
	if rep.Status != "handed_off" {
		t.Fatalf("status = %q want handed_off", rep.Status)
	}
}

func TestAutonomous_WallClockBudget(t *testing.T) {
	d := &fakeDriver{max: 100000}
	c := runnerContract([]string{"a"})
	c.Budget.WallSeconds = 1 * time.Millisecond
	r, _ := NewAutonomousRunner(AutonomousConfig{Contract: c, Driver: d})
	time.Sleep(5 * time.Millisecond) // ensure deadline already passed when Run starts
	rep, _ := r.Run(context.Background())
	if rep.Status != "exhausted" {
		t.Fatalf("status = %q want exhausted", rep.Status)
	}
}

func TestAutonomous_StatusMidFlight(t *testing.T) {
	d := &fakeDriver{max: 100000}
	c := runnerContract([]string{"never-satisfied"})
	c.Budget.Steps = 1000
	r, _ := NewAutonomousRunner(AutonomousConfig{Contract: c, Driver: d})
	go r.Run(context.Background())
	time.Sleep(20 * time.Millisecond)
	st := r.Status()
	if st.ContractID != c.ID {
		t.Fatalf("contract id = %q want %q", st.ContractID, c.ID)
	}
	if len(st.Pending) == 0 {
		t.Fatal("expected pending specs")
	}
	r.Cancel()
}

func TestNewAutonomousRunner_RejectsBadContract(t *testing.T) {
	_, err := NewAutonomousRunner(AutonomousConfig{Contract: nil, Driver: &fakeDriver{}})
	if err == nil {
		t.Fatal("nil contract must error")
	}
	bad := &Contract{ID: "x", Goal: "x"} // missing outputs
	_, err = NewAutonomousRunner(AutonomousConfig{Contract: bad, Driver: &fakeDriver{}})
	if err == nil {
		t.Fatal("invalid contract must error")
	}
}

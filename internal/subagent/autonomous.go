// Package subagent — autonomous-execution loop.
//
// AutonomousRunner takes a frozen Contract and drives a child Step interface
// to completion. It auto-compacts when context fills, watches budget, and
// hands back a structured FinalReport when the contract is satisfied,
// exhausted, violated, or handed off.
package subagent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// StepResult is the agent-agnostic outcome of one driver step.
type StepResult struct {
	// ToolCalls executed during the step (used for step counting).
	ToolCalls int
	// Tokens used (cumulative or delta — runner only inspects the
	// CurrentTokens snapshot from BudgetSnapshot).
	Tokens int
	// Done is true when the underlying agent reports task completion (e.g. a
	// final assistant turn with no further tool calls).
	Done bool
	// Output is the assistant text produced this step (last seen).
	Output string
	// Err, if non-nil, is a per-step error. Tool-level ContractViolations
	// should be returned as an error implementing the violation interface
	// so the runner can surface them in FinalReport.Violations.
	Err error
}

// BudgetSnapshot is the runner's view of the child's context budget.
type BudgetSnapshot struct {
	CurrentTokens int
	MaxTokens     int
}

// Utilization is convenience math for the snapshot.
func (b BudgetSnapshot) Utilization() float64 {
	if b.MaxTokens <= 0 {
		return 0
	}
	return float64(b.CurrentTokens) / float64(b.MaxTokens)
}

// StepDriver is the small interface AutonomousRunner consumes. Real agents
// (internal/agent.Agent) adapt to this via a thin shim; tests provide fakes.
type StepDriver interface {
	// Step performs exactly one unit of work. Implementations should not
	// loop internally — the runner owns the iteration.
	Step(ctx context.Context) StepResult
	// Budget returns a current snapshot of token usage.
	Budget() BudgetSnapshot
	// Compact attempts to reduce context to fit. Implementations should
	// return the post-compaction BudgetSnapshot.
	Compact(ctx context.Context) (BudgetSnapshot, error)
	// CheckOutput returns the list of ExpectedOutputs (by Spec) that have
	// been satisfied so far. Implementations may use simple file-existence
	// checks for OutFile, or model-side heuristics for other kinds.
	CheckOutput(ctx context.Context, outputs []Output) (satisfiedSpecs []string)
}

// StatusReport is a mid-flight snapshot of the runner.
type StatusReport struct {
	ContractID            string              `json:"contract_id"`
	Done                  []string            `json:"done"`
	Pending               []string            `json:"pending"`
	Violations            []ContractViolation `json:"violations,omitempty"`
	TokensUsed            int                 `json:"tokens_used"`
	StepsUsed             int                 `json:"steps_used"`
	Elapsed               time.Duration       `json:"elapsed"`
	NextRecommendedAction string              `json:"next_recommended_action"`
}

// FinalReport is the terminal outcome of a Run.
type FinalReport struct {
	ContractID string              `json:"contract_id"`
	Status     string              `json:"status"` // completed | violated | exhausted | handed_off | failed
	Outputs    []OutputResult      `json:"outputs"`
	Acceptance []AcceptanceResult  `json:"acceptance"`
	Violations []ContractViolation `json:"violations,omitempty"`
	TokensUsed int                 `json:"tokens_used"`
	StepsUsed  int                 `json:"steps_used"`
	Elapsed    time.Duration       `json:"elapsed"`
	Reason     string              `json:"reason,omitempty"`
	LastOutput string              `json:"last_output,omitempty"`
}

// OutputResult is the per-ExpectedOutput verdict.
type OutputResult struct {
	Spec      string `json:"spec"`
	Kind      string `json:"kind"`
	Satisfied bool   `json:"satisfied"`
}

// AutonomousRunner drives a StepDriver against a Contract.
type AutonomousRunner struct {
	contract *Contract
	driver   StepDriver
	workdir  string
	runner   AcceptanceRunner

	// handoff is closed when the runner emits a final or interim handoff
	// status report. May be nil in fire-and-forget mode.
	handoff chan StatusReport

	// state
	mu          sync.RWMutex
	startedAt   time.Time
	stepsUsed   int32
	tokensUsed  int32
	done        []string
	violations  []ContractViolation
	finalReport *FinalReport
	lastOutput  string
	cancelled   atomic.Bool
}

// AutonomousConfig configures an AutonomousRunner.
type AutonomousConfig struct {
	Contract *Contract
	Driver   StepDriver
	Workdir  string
	// Runner overrides AcceptanceRunner; defaults to DefaultAcceptanceRunner.
	Runner AcceptanceRunner
	// Handoff, if non-nil, receives one StatusReport on handoff/exhaustion.
	// Buffered channel recommended (size 1).
	Handoff chan StatusReport
}

// NewAutonomousRunner constructs a runner. Validates the contract.
func NewAutonomousRunner(cfg AutonomousConfig) (*AutonomousRunner, error) {
	if cfg.Contract == nil {
		return nil, fmt.Errorf("autonomous: contract is required")
	}
	if cfg.Driver == nil {
		return nil, fmt.Errorf("autonomous: driver is required")
	}
	if err := cfg.Contract.Validate(); err != nil {
		return nil, fmt.Errorf("autonomous: %w", err)
	}
	return &AutonomousRunner{
		contract: cfg.Contract,
		driver:   cfg.Driver,
		workdir:  cfg.Workdir,
		runner:   cfg.Runner,
		handoff:  cfg.Handoff,
	}, nil
}

// Cancel marks the runner as cancelled. Run loop checks on each iteration.
func (r *AutonomousRunner) Cancel() { r.cancelled.Store(true) }

// Status returns a copy of the current StatusReport.
func (r *AutonomousRunner) Status() StatusReport {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.snapshotStatus()
}

func (r *AutonomousRunner) snapshotStatus() StatusReport {
	pending := r.pendingSpecs()
	rec := "continue"
	if len(r.violations) > 0 {
		rec = "abort"
	} else if len(pending) == 0 {
		rec = "ask_user"
	}
	return StatusReport{
		ContractID:            r.contract.ID,
		Done:                  append([]string(nil), r.done...),
		Pending:               pending,
		Violations:            append([]ContractViolation(nil), r.violations...),
		TokensUsed:            int(atomic.LoadInt32(&r.tokensUsed)),
		StepsUsed:             int(atomic.LoadInt32(&r.stepsUsed)),
		Elapsed:               time.Since(r.startedAt),
		NextRecommendedAction: rec,
	}
}

func (r *AutonomousRunner) pendingSpecs() []string {
	doneSet := make(map[string]bool, len(r.done))
	for _, d := range r.done {
		doneSet[d] = true
	}
	pending := make([]string, 0)
	for _, o := range r.contract.ExpectedOutputs {
		if !doneSet[o.Spec] {
			pending = append(pending, o.Spec)
		}
	}
	return pending
}

// FinalReport returns the terminal report once Run has finished. Nil while
// the runner is still active.
func (r *AutonomousRunner) FinalReport() *FinalReport {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.finalReport
}

// Run drives the StepDriver to completion. It NEVER asks the user for input;
// when the contract cannot be satisfied autonomously it returns a FinalReport
// describing why.
func (r *AutonomousRunner) Run(ctx context.Context) (*FinalReport, error) {
	r.mu.Lock()
	r.startedAt = time.Now()
	r.mu.Unlock()

	wallDeadline := time.Time{}
	if r.contract.Budget.WallSeconds > 0 {
		wallDeadline = r.startedAt.Add(r.contract.Budget.WallSeconds)
	}

	for {
		// Cancellation/context check.
		if r.cancelled.Load() {
			return r.finalize("handed_off", "cancelled by parent"), nil
		}
		if err := ctx.Err(); err != nil {
			return r.finalize("handed_off", "context cancelled: "+err.Error()), nil
		}

		// Wall-clock budget check.
		if !wallDeadline.IsZero() && time.Now().After(wallDeadline) {
			return r.exhaust("wall-clock budget exceeded"), nil
		}

		// Token budget — auto-compact at 80% utilization, hard-stop at 100%.
		bs := r.driver.Budget()
		atomic.StoreInt32(&r.tokensUsed, int32(bs.CurrentTokens))
		if bs.MaxTokens > 0 && bs.CurrentTokens >= bs.MaxTokens {
			return r.exhaust(fmt.Sprintf("token budget exhausted (%d/%d)", bs.CurrentTokens, bs.MaxTokens)), nil
		}
		if bs.Utilization() >= 0.80 {
			if r.contract.OnContextFull == OnContextFullFail {
				return r.exhaust(fmt.Sprintf("context full (%.0f%%) and policy=fail", bs.Utilization()*100)), nil
			}
			if r.contract.OnContextFull == OnContextFullHandoffToParent {
				return r.handoffNow(fmt.Sprintf("context full (%.0f%%) — handing off per policy", bs.Utilization()*100)), nil
			}
			// Default: compact_and_continue.
			newBs, err := r.driver.Compact(ctx)
			if err != nil {
				return r.handoffNow("compaction failed: " + err.Error()), nil
			}
			if newBs.MaxTokens > 0 && newBs.Utilization() >= 0.80 {
				return r.handoffNow(fmt.Sprintf("post-compact still at %.0f%%", newBs.Utilization()*100)), nil
			}
		}

		// Contract step budget.
		if r.contract.Budget.Steps > 0 && int(atomic.LoadInt32(&r.stepsUsed)) >= r.contract.Budget.Steps {
			return r.exhaust(fmt.Sprintf("step budget exhausted (%d)", r.contract.Budget.Steps)), nil
		}

		// Drive one step.
		sr := r.driver.Step(ctx)
		atomic.AddInt32(&r.stepsUsed, 1)
		if sr.Output != "" {
			r.mu.Lock()
			r.lastOutput = sr.Output
			r.mu.Unlock()
		}

		// Step error: surface ContractViolation specifically.
		if sr.Err != nil {
			if v, ok := asContractViolation(sr.Err); ok {
				r.mu.Lock()
				r.violations = append(r.violations, v)
				r.mu.Unlock()
				return r.finalize("violated", v.Error()), nil
			}
			return r.finalize("failed", sr.Err.Error()), nil
		}

		// Update done set.
		r.refreshDone(ctx)

		// All ExpectedOutputs satisfied → run final acceptance.
		if len(r.pendingSpecs()) == 0 {
			return r.complete(ctx), nil
		}

		// Driver reported done but pending specs remain → contract violation.
		if sr.Done {
			pending := r.pendingSpecs()
			v := ContractViolation{
				Criterion: "expected_outputs",
				Reason:    "driver completed without satisfying all expected outputs",
				Evidence:  "pending: " + strings.Join(pending, ", "),
			}
			r.mu.Lock()
			r.violations = append(r.violations, v)
			r.mu.Unlock()
			return r.finalize("violated", v.Reason), nil
		}
	}
}

func (r *AutonomousRunner) refreshDone(ctx context.Context) {
	got := r.driver.CheckOutput(ctx, r.contract.ExpectedOutputs)
	r.mu.Lock()
	defer r.mu.Unlock()
	seen := make(map[string]bool, len(r.done))
	for _, d := range r.done {
		seen[d] = true
	}
	for _, g := range got {
		if !seen[g] {
			r.done = append(r.done, g)
			seen[g] = true
		}
	}
}

func (r *AutonomousRunner) complete(ctx context.Context) *FinalReport {
	acc := r.contract.RunAcceptance(ctx, r.workdir, r.runner)
	allPass := true
	for _, a := range acc {
		if !a.Passed {
			allPass = false
			break
		}
	}
	status := "completed"
	reason := ""
	if !allPass {
		status = "violated"
		reason = "acceptance checks failed"
		// Convert failed acceptance to violations.
		for _, a := range acc {
			if !a.Passed {
				r.violations = append(r.violations, ContractViolation{
					Criterion: "acceptance:" + a.Name,
					Reason:    a.Reason,
				})
			}
		}
	}
	rep := r.buildReport(status, reason)
	rep.Acceptance = acc
	r.mu.Lock()
	r.finalReport = rep
	r.mu.Unlock()
	return rep
}

func (r *AutonomousRunner) finalize(status, reason string) *FinalReport {
	rep := r.buildReport(status, reason)
	r.mu.Lock()
	r.finalReport = rep
	r.mu.Unlock()
	r.maybeEmitHandoff()
	return rep
}

func (r *AutonomousRunner) exhaust(reason string) *FinalReport {
	return r.finalize("exhausted", reason)
}

func (r *AutonomousRunner) handoffNow(reason string) *FinalReport {
	return r.finalize("handed_off", reason)
}

func (r *AutonomousRunner) buildReport(status, reason string) *FinalReport {
	r.mu.RLock()
	defer r.mu.RUnlock()
	doneSet := make(map[string]bool, len(r.done))
	for _, d := range r.done {
		doneSet[d] = true
	}
	outputs := make([]OutputResult, 0, len(r.contract.ExpectedOutputs))
	for _, o := range r.contract.ExpectedOutputs {
		outputs = append(outputs, OutputResult{
			Spec:      o.Spec,
			Kind:      string(o.Kind),
			Satisfied: doneSet[o.Spec],
		})
	}
	return &FinalReport{
		ContractID: r.contract.ID,
		Status:     status,
		Outputs:    outputs,
		Violations: append([]ContractViolation(nil), r.violations...),
		TokensUsed: int(atomic.LoadInt32(&r.tokensUsed)),
		StepsUsed:  int(atomic.LoadInt32(&r.stepsUsed)),
		Elapsed:    time.Since(r.startedAt),
		Reason:     reason,
		LastOutput: r.lastOutput,
	}
}

func (r *AutonomousRunner) maybeEmitHandoff() {
	if r.handoff == nil {
		return
	}
	st := r.snapshotStatus()
	select {
	case r.handoff <- st:
	default:
	}
}

// asContractViolation tries to recover a ContractViolation from any error.
func asContractViolation(err error) (ContractViolation, bool) {
	if err == nil {
		return ContractViolation{}, false
	}
	if v, ok := err.(ContractViolation); ok {
		return v, true
	}
	return ContractViolation{}, false
}

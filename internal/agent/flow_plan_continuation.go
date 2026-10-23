package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

// LoadedFlowPlan contains scheduler data only. It carries no tool callbacks,
// approval capability, provider credential, or grant of autonomous authority.
type LoadedFlowPlan struct {
	RequireEvaluation bool    `json:"require_evaluation,omitempty"`
	MinConfidence     float64 `json:"min_confidence,omitempty"`
	MaxIterations     int     `json:"max_iterations,omitempty"`
	MaxRevisionDepth  int     `json:"max_revision_depth,omitempty"`
	Plan              Plan    `json:"plan"`
	CurrentPhase      int     `json:"current_phase"`
	Started           bool    `json:"started"`
}

func (a *Agent) snapshotFlowPlan(state *FlowState) {
	a.mu.RLock()
	am := a.autoMode
	a.mu.RUnlock()
	if am == nil || am.Plan == nil || am.CurrentPhase < 0 || am.CurrentPhase >= len(am.Plan.Phases) {
		return
	}
	phase := am.Plan.Phases[am.CurrentPhase]
	started := phase.Status == PhaseRunning || (phase.Status == PhaseFailed && state.Reason == "deadline_exceeded")
	if !started && !(phase.Status == PhaseDeferred && strings.HasPrefix(state.Reason, "exceeded max steps")) {
		return
	}
	plan := *am.Plan
	plan.Phases = append([]PlanPhase(nil), am.Plan.Phases...)
	state.LoadedPlan = &LoadedFlowPlan{Plan: plan, CurrentPhase: am.CurrentPhase, Started: started}
	if am.ShouldSelfEvaluate() {
		state.LoadedPlan.RequireEvaluation = true
		state.LoadedPlan.MinConfidence = am.SelfEval.MinConfidence
		state.LoadedPlan.MaxIterations = am.SelfEval.MaxIterations
		state.LoadedPlan.MaxRevisionDepth = am.SelfEval.MaxRevisionDepth
	}
}
func (a *Agent) restoreFlowPlan(ctx context.Context, state *FlowState) (context.Context, error) {
	checkpoint := state.LoadedPlan
	if checkpoint == nil {
		return ctx, nil
	}
	plan, err := validateLoadedFlowPlan(state)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	am := a.autoMode
	if am == nil {
		am = NewAutoMode(string(AutonomySupervised))
	} else {
		copy := *am
		am = &copy
	}
	// Preserve CURRENT runtime policy and callbacks. Persisted scheduler data
	// cannot enable auto mode or replace the active application's tool authority.
	if checkpoint.RequireEvaluation {
		if checkpoint.MinConfidence < 0 || checkpoint.MinConfidence > 1 || checkpoint.MaxIterations < 1 || checkpoint.MaxIterations > 100 || checkpoint.MaxRevisionDepth < 0 || checkpoint.MaxRevisionDepth > 100 {
			a.mu.Unlock()
			return nil, fmt.Errorf("flow evaluation policy invalid")
		}
		if am.SelfEval == nil {
			am.SelfEval = NewSelfEvaluateLoop(nil, a.recovery, nil)
		}
		am.RequirePhaseEvaluation = true
		if checkpoint.MinConfidence > am.SelfEval.MinConfidence {
			am.SelfEval.MinConfidence = checkpoint.MinConfidence
		}
		if checkpoint.MaxIterations < am.SelfEval.MaxIterations {
			am.SelfEval.MaxIterations = checkpoint.MaxIterations
		}
		if checkpoint.MaxRevisionDepth < am.SelfEval.MaxRevisionDepth {
			am.SelfEval.MaxRevisionDepth = checkpoint.MaxRevisionDepth
		}
	}
	am.Plan = &plan
	am.CurrentPhase = checkpoint.CurrentPhase
	am.Plan.Phases[am.CurrentPhase].Status = PhaseRunning
	a.autoMode = am
	a.mu.Unlock()
	if !checkpoint.Started {
		a.appendMessage(providers.Message{Role: "user", Content: loadedPhasePrompt(am, &am.Plan.Phases[am.CurrentPhase])})
	}
	return context.WithValue(ctx, loadedPlanKey{}, &loadedPlanExecution{mode: am}), nil
}
func (a *Agent) activateFlowPlan(ctx context.Context) {
	if execution, ok := ctx.Value(loadedPlanKey{}).(*loadedPlanExecution); ok && execution != nil {
		a.observePlan(execution.mode, "running", "Resumed from a durable task checkpoint.")
	}
}

func validateLoadedFlowPlan(state *FlowState) (Plan, error) {
	checkpoint := state.LoadedPlan
	if checkpoint == nil {
		return Plan{}, nil
	}
	plan := checkpoint.Plan
	plan.Phases = append([]PlanPhase(nil), checkpoint.Plan.Phases...)
	if len(plan.Phases) < 1 || len(plan.Phases) > 100 || checkpoint.CurrentPhase < 0 || checkpoint.CurrentPhase >= len(plan.Phases) {
		return Plan{}, fmt.Errorf("flow plan checkpoint invalid")
	}
	for index, phase := range plan.Phases {
		if phase.Index != index {
			return Plan{}, fmt.Errorf("flow plan phase index invalid")
		}
		if index < checkpoint.CurrentPhase && phase.Status != PhaseDone && phase.Status != PhaseSkipped {
			return Plan{}, fmt.Errorf("flow plan previous phase was not accepted")
		}
		if index > checkpoint.CurrentPhase && phase.Status != PhasePending {
			return Plan{}, fmt.Errorf("flow plan future phase state invalid")
		}
	}
	activeStatus := plan.Phases[checkpoint.CurrentPhase].Status
	if checkpoint.Started {
		if activeStatus != PhaseRunning && !(activeStatus == PhaseFailed && state.Reason == "deadline_exceeded") {
			return Plan{}, fmt.Errorf("flow plan active phase state invalid")
		}
	} else if activeStatus != PhaseDeferred {
		return Plan{}, fmt.Errorf("flow plan pending phase state invalid")
	}

	if checkpoint.RequireEvaluation && (checkpoint.MinConfidence < 0 || checkpoint.MinConfidence > 1 || checkpoint.MaxIterations < 1 || checkpoint.MaxIterations > 100 || checkpoint.MaxRevisionDepth < 0 || checkpoint.MaxRevisionDepth > 100) {
		return Plan{}, fmt.Errorf("flow evaluation policy invalid")
	}
	return plan, nil
}

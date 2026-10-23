package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

// PlanExecutionObservation records scheduler outcomes. A done phase means its
// configured completion policy accepted; it is not independent proof of success.
// Snapshots are persisted with runtime status, never used to auto-resume work.
type PlanExecutionObservation struct {
	Current bool                   `json:"current"`
	Source  string                 `json:"source"`
	Title   string                 `json:"title"`
	Status  string                 `json:"status"`
	Phases  []PlanPhaseObservation `json:"phases"`
}
type PlanPhaseObservation struct {
	Title  string `json:"title"`
	Status string `json:"status"`
	Note   string `json:"note,omitempty"`
}

func clonePlanExecution(p *PlanExecutionObservation) *PlanExecutionObservation {
	if p == nil {
		return nil
	}
	c := *p
	c.Phases = append([]PlanPhaseObservation(nil), p.Phases...)
	return &c
}

type loadedPlanExecution struct{ mode *AutoMode }
type loadedPlanKey struct{}

func (a *Agent) observePlan(am *AutoMode, status, note string) {
	observation := &PlanExecutionObservation{Source: "loaded_plan_executor", Status: status, Current: true}
	observation.Title, _ = boundedVerificationText(am.Plan.Title, 512)
	for _, phase := range am.Plan.Phases {
		title, _ := boundedVerificationText(phase.Title, 512)
		p := PlanPhaseObservation{Title: title, Status: phase.Status.String()}
		if phase.Status == PhasePending && (status == "failed" || status == "deferred") {
			p.Note = fmt.Sprintf("Not started; execution stopped at phase %d.", am.CurrentPhase+1)
		}
		if phase.Index == am.CurrentPhase {
			p.Note, _ = boundedVerificationText(note, 2048)
		}
		observation.Phases = append(observation.Phases, p)
	}
	state := &a.verificationObservations
	state.mu.Lock()
	state.plan = observation
	state.mu.Unlock()
	a.persistRuntimeStatus()
	a.emit("plan_execution", map[string]any{"session_id": a.SessionID(), "plan": clonePlanExecution(observation)})
}

// Only a newly loaded plan starts automatically. Failed/deferred/interrupted
// plans require an explicit reload; another user message never resumes them.
func (a *Agent) beginLoadedPlan(ctx context.Context, input string) (context.Context, string, error) {
	a.mu.RLock()
	am := a.autoMode
	a.mu.RUnlock()
	if am == nil || !am.IsAuto() || am.Plan == nil || am.CurrentPhase != -1 {
		return ctx, input, nil
	}
	if len(am.Plan.Phases) == 0 || len(am.Plan.Phases) > 100 {
		return ctx, input, fmt.Errorf("agent: loaded plans require 1–100 phases")
	}
	for _, p := range am.Plan.Phases {
		if p.Status != PhasePending {
			return ctx, input, fmt.Errorf("agent: reload a fresh plan before execution")
		}
	}
	phase := am.NextPhase()
	a.observePlan(am, "running", "")
	ctx = context.WithValue(ctx, loadedPlanKey{}, &loadedPlanExecution{mode: am})
	return ctx, input + "\n\n" + loadedPhasePrompt(am, phase), nil
}
func loadedPhasePrompt(am *AutoMode, p *PlanPhase) string {
	return fmt.Sprintf("Loaded plan: %s\nExecute ONLY phase %d of %d: %s\n\n%s\n\nReport this phase's result and unresolved blockers truthfully. The runtime schedules subsequent phases after completion assessment; do not execute subsequent phases yourself.", am.Plan.Title, p.Index+1, len(am.Plan.Phases), p.Title, p.Description)
}

// finishLoadedPlan closes any in-flight phase on every exit, including provider
// errors, cancellation, estop and initial-step exhaustion. Pending phases stay
// pending; no whole-plan success is inferred from one phase's answer.
func (a *Agent) finishLoadedPlan(ctx context.Context) {
	execution, _ := ctx.Value(loadedPlanKey{}).(*loadedPlanExecution)
	if execution == nil {
		return
	}
	am := execution.mode
	if am.Plan.Phases[am.CurrentPhase].Status == PhaseRunning {
		am.MarkPhaseFailed()
		note := "Execution interrupted or failed before phase acceptance; reload explicitly to retry."
		if ctx.Err() != nil {
			note = ctx.Err().Error()
		}
		a.observePlan(am, "failed", note)
		am.FireCompletion(&SelfEvalResult{FinalNote: note})
	}
}

func (a *Agent) evaluateCompletion(ctx context.Context, input string, result *RunResult, notify func(string)) error {
	execution, _ := ctx.Value(loadedPlanKey{}).(*loadedPlanExecution)
	if execution == nil {
		return a.evaluateSingleCompletion(ctx, input, result, notify)
	}
	am := execution.mode
	defer func() { a.finishLoadedPlan(ctx); result.Plan = a.RuntimeStatusSnapshot().Plan }()
	for {
		phase := &am.Plan.Phases[am.CurrentPhase]
		err := a.evaluateSingleCompletion(ctx, phase.Title+"\n"+phase.Description, result, notify)
		if err == nil {
			err = ctx.Err()
		}
		if err == nil {
			select {
			case <-a.StopCh():
				err = fmt.Errorf("agent: halted by estop")
			default:
			}
		}
		if errors.Is(err, errStepBudget) {
			if ckErr := a.checkpointRunExit(input, result.Steps, fmt.Sprintf("exceeded max steps (%d) during loaded plan revision", a.maxSteps)); ckErr != nil {
				return fmt.Errorf("loaded plan revision checkpoint failed: %w", ckErr)
			}
		}
		if err != nil || result.Blocked {
			phase.Status = PhaseFailed
			status, note := "failed", result.BlockReason
			if err != nil {
				note = err.Error()
			} else if strings.HasPrefix(result.BlockReason, "Self-evaluation deferred:") {
				status = "deferred"
				phase.Status = PhaseDeferred
			}
			a.observePlan(am, status, note)
			am.FireCompletion(&SelfEvalResult{FinalNote: note})
			return err
		}
		am.MarkPhaseDone()
		a.observePlan(am, "running", "Completion policy accepted this phase; independent verification is reported separately.")
		next := am.NextPhase()
		if next == nil {
			a.observePlan(am, "completed", "")
			am.FireCompletion(&SelfEvalResult{Accepted: true, FinalNote: "All loaded phases accepted by the configured completion policy."})
			return nil
		}
		if result.Steps >= a.maxSteps {
			next.Status = PhaseDeferred
			note := fmt.Sprintf("Loaded plan exhausted its shared max steps (%d); this phase was not started.", a.maxSteps)
			if err := a.checkpointRunExit(input, result.Steps, fmt.Sprintf("exceeded max steps (%d) during loaded plan", a.maxSteps)); err != nil {
				return fmt.Errorf("loaded plan checkpoint failed: %w", err)
			}
			result.Blocked, result.BlockReason = true, note
			a.observePlan(am, "deferred", note)
			am.FireCompletion(&SelfEvalResult{FinalNote: note})
			return fmt.Errorf("%w (%d) during loaded plan", errStepBudget, a.maxSteps)
		}
		a.observePlan(am, "running", "")
		if notify != nil {
			notify("plan_phase_running")
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		a.appendMessage(providers.Message{Role: "user", Content: loadedPhasePrompt(am, next)})
		if err := a.completeAdditionalWork(ctx, result); err != nil {
			return err
		}
	}
}

func (a *Agent) hasFreshLoadedPlan() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.autoMode != nil && a.autoMode.IsAuto() && a.autoMode.Plan != nil && a.autoMode.CurrentPhase == -1
}

// A later admitted turn keeps plan history available without presenting the
// old plan's terminal outcome as this turn's evaluation result.
func (a *Agent) archivePlanObservation() {
	state := &a.verificationObservations
	state.mu.Lock()
	if state.plan == nil || !state.plan.Current {
		state.mu.Unlock()
		return
	}
	observation := clonePlanExecution(state.plan)
	observation.Current = false
	state.plan = observation
	state.mu.Unlock()
	a.persistRuntimeStatus()
	a.emit("plan_execution", map[string]any{"session_id": a.SessionID(), "plan": clonePlanExecution(observation)})
}

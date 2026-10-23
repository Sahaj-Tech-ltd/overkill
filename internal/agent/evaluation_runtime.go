package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

// IterativeEvaluationObservation describes an actually invoked optional loop.
// Acceptance means the loop's confidence policy accepted, not that tests passed.
type IterativeEvaluationObservation struct {
	Source        string `json:"source"`
	Status        string `json:"status"`
	Phase         string `json:"phase"`
	Iteration     int    `json:"iteration"`
	MaxIterations int    `json:"max_iterations"`
	StartedAt     string `json:"started_at"`
	CompletedAt   string `json:"completed_at,omitempty"`
	Note          string `json:"note,omitempty"`
}

func cloneIterative(v *IterativeEvaluationObservation) *IterativeEvaluationObservation {
	if v == nil {
		return nil
	}
	c := *v
	return &c
}

func (a *Agent) observeIteration(phase string, iteration, max int, notify func(string)) {
	state := &a.verificationObservations
	state.mu.Lock()
	e := cloneIterative(state.iterative)
	if e == nil || (iteration == 1 && phase == "executing") {
		e = &IterativeEvaluationObservation{Source: "iterative_self_evaluation", StartedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	}
	e.Status, e.Phase, e.Iteration, e.MaxIterations = "running", phase, iteration, max
	state.iterative = e
	state.mu.Unlock()
	a.persistRuntimeStatus()
	a.emit("self_evaluation", map[string]any{"session_id": a.SessionID(), "evaluation": cloneIterative(e)})
	if notify != nil {
		notify("self_evaluation_" + phase)
	}
}

// evaluateCompletion reuses the first successful answer, then performs revisions
// within the existing admission, goal accounting context and total step limit.
// Streaming callers receive revision statuses and the final authoritative result;
// revision provider calls use Complete, so they do not produce token deltas.
func (a *Agent) evaluateSingleCompletion(ctx context.Context, input string, result *RunResult, notify func(string)) error {
	if result.Blocked {
		return nil
	}
	a.mu.RLock()
	am := a.autoMode
	var loop *SelfEvaluateLoop
	if am.ShouldSelfEvaluate() {
		loop = am.SelfEval
	}
	a.mu.RUnlock()
	if loop == nil {
		return nil
	}
	first := true
	eval, _, err := loop.runPhase(ctx, &PlanPhase{Title: input, Description: input}, a.Model(), func(ctx context.Context, _ string, revision string) ([]providers.Message, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !first {
			if revision == "" {
				revision = "Review the previous answer for unresolved gaps and correct them. The confidence assessment did not meet the configured acceptance threshold."
			}
			a.appendMessage(providers.Message{Role: "user", Content: revision})
			if err := a.completeAdditionalWork(ctx, result); err != nil {
				return nil, err
			}
		}
		first = false
		a.mu.RLock()
		history := append([]providers.Message(nil), a.history...)
		a.mu.RUnlock()
		return history, nil
	}, func(phase string, iteration, max int) { a.observeIteration(phase, iteration, max, notify) }, true)
	state := &a.verificationObservations
	state.mu.Lock()
	observation := cloneIterative(state.iterative)
	if observation != nil {
		observation.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
		observation.Status, observation.Phase = "failed", "complete"
		if eval != nil {
			observation.Iteration = eval.Iterations
			observation.Note, _ = boundedVerificationText(eval.FinalNote, 2048)
			if err == nil {
				observation.Status = "deferred"
				if eval.Accepted {
					observation.Status = "accepted"
				}
			}
		}
		state.iterative = observation
	}
	state.mu.Unlock()
	a.persistRuntimeStatus()
	a.emit("self_evaluation", map[string]any{"session_id": a.SessionID(), "evaluation": cloneIterative(observation)})
	if err == nil && eval != nil && !eval.Accepted {
		result.Blocked, result.BlockReason = true, "Self-evaluation deferred: "+eval.FinalNote
	}
	return err
}

// completeAdditionalWork shares the admitted turn's remaining provider-step budget.
func (a *Agent) completeAdditionalWork(ctx context.Context, result *RunResult) error {
	done := false
	for result.Steps < a.maxSteps {
		if err := ctx.Err(); err != nil {
			return err
		}
		select {
		case <-a.StopCh():
			return fmt.Errorf("agent: halted by estop")
		default:
		}
		if err := a.checkBudget(ctx); err != nil {
			return err
		}
		step, err := a.step(ctx)
		if err != nil {
			a.emitRecovery(err)
			return err
		}
		result.Steps++
		a.sessionMetrics.mu.Lock()
		a.sessionMetrics.toolCalls += len(step.ToolCalls)
		a.sessionMetrics.turns++
		a.sessionMetrics.mu.Unlock()
		result.ToolCalls += len(step.ToolCalls)
		result.TotalTokens += step.Tokens.InputTokens + step.Tokens.OutputTokens
		if obs := a.usageObserver.Load(); obs != nil && (step.Tokens.InputTokens > 0 || step.Tokens.OutputTokens > 0) {
			func() { defer func() { _ = recover() }(); obs.fn(a.Model(), step.Tokens) }()
		}
		if step.Done {
			result.Response, result.Model = step.Thought, a.Model()
			if step.VerificationFailure != "" {
				result.Blocked, result.BlockReason = true, step.VerificationFailure
				return fmt.Errorf("agent: self-evaluation revision verification failed: %s", step.VerificationFailure)
			}
			done = true
			break
		}
	}
	if !done {
		return fmt.Errorf("%w (%d) during self-evaluation", errStepBudget, a.maxSteps)
	}
	return nil
}

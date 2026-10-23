package agent

import (
	"context"
	"errors"
	"fmt"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/security"
)

// ContinueStream resumes a trusted application's admitted durable checkpoint.
// It does not append the original user input or invoke slash-command handlers.
// Callers must verify session/workspace/model and own exclusive turn admission.
func (a *Agent) ContinueStream(ctx context.Context, state *FlowState) (<-chan StreamEvent, error) {
	if state == nil || state.SessionID != a.SessionID() || state.Model != a.Model() {
		return nil, fmt.Errorf("flow continuation identity mismatch")
	}
	if a.running.Swap(true) {
		return nil, fmt.Errorf("agent: already running — wait for current turn to finish")
	}
	delegated := false
	defer func() {
		if !delegated {
			a.running.Store(false)
		}
	}()
	if state.SessionID != a.SessionID() || state.Model != a.Model() {
		return nil, fmt.Errorf("flow continuation identity mismatch")
	}
	if !flowHistoryComplete(state.History) {
		return nil, fmt.Errorf("flow continuation contains unresolved tool execution; review required")
	}
	if _, err := validateLoadedFlowPlan(state); err != nil {
		return nil, err
	}
	if err := validateSequentialCheckpoint(state.Sequential); err != nil {
		return nil, err
	}
	if state.Sequential != nil && state.LoadedPlan != nil {
		return nil, fmt.Errorf("flow contains conflicting schedulers")
	}
	if state.ReadOnly {
		a.planReadonly.Store(true)
	}
	a.mu.RLock()
	store := a.flowStore
	a.mu.RUnlock()
	if bound, ok := store.(interface{ ContinueTurn(int64) }); ok {
		bound.ContinueTurn(state.CancelEpoch)
	}
	a.RestoreHistory(state.History)
	var err error
	ctx, err = a.restoreFlowPlan(ctx, state)
	if err != nil {
		return nil, err
	}
	delegated = true
	if state.Sequential != nil {
		return a.streamSequential(ctx, state.Sequential.Input, state.Sequential)
	}
	return a.streamWithAttachments(ctx, state.UserInput, nil, true, true)
}
func (a *Agent) flowCheckpointMetadata(state *FlowState) {
	state.ReadOnly = a.PrivilegeMode() == security.ModeReader
	state.Workspace = a.mentionWorkspace
	a.snapshotFlowPlan(state)
	a.snapshotSequentialFlow(state)
}
func (a *Agent) checkpointContextExit(ctx context.Context, userInput string, step int, reason string) {
	if errors.Is(context.Cause(ctx), errAgentEmergencyStop) {
		reason = "halted_by_estop"
	} else if ctx.Err() == context.DeadlineExceeded {
		reason = "deadline_exceeded"
	}
	a.checkpointInterrupt(userInput, step, reason)
}

func (a *Agent) beginFlowTurn(ctx context.Context) error {
	a.mu.RLock()
	store := a.flowStore
	a.mu.RUnlock()
	if bound, ok := store.(interface{ BeginHumanTurn(context.Context) error }); ok {
		return bound.BeginHumanTurn(ctx)
	}
	if bound, ok := store.(interface{ BeginTurn(context.Context) error }); ok {
		return bound.BeginTurn(ctx)
	}
	return nil
}

// checkpointRunExit is shared by nonstreaming timeout/exhaustion paths.
func (a *Agent) checkpointRunExit(input string, step int, reason string) error {
	input = a.sequentialCheckpointInput(input)
	a.mu.RLock()
	store := a.flowStore
	model := a.model
	history := append([]providers.Message(nil), a.history...)
	a.mu.RUnlock()
	if store == nil {
		return nil
	}
	_, err := CheckpointFlow(store, flowIDFor(a.SessionID(), input), a.SessionID(), input, model, "", history, step, reason, a.flowCheckpointMetadata)
	return err
}

var errAgentEmergencyStop = errors.New("agent: halted by estop")

func (a *Agent) stopAwareRunContext(parent context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancelCause(parent)
	done := make(chan struct{})
	stop := a.StopCh()
	select {
	case <-stop:
		cancel(errAgentEmergencyStop)
	default:
	}
	go func() {
		defer close(done)
		select {
		case <-ctx.Done():
		case <-stop:
			cancel(errAgentEmergencyStop)
		}
	}()
	return ctx, func() { cancel(nil); <-done }
}

func flowCheckpointNotice(state *FlowState) string {
	switch state.ResumeStatus {
	case "exhausted":
		return fmt.Sprintf("Task checkpoint saved (flow %s). Automatic resume limit reached; review the remaining work.", state.ID)
	case "manual_review":
		return fmt.Sprintf("Task checkpoint saved (flow %s). Review unresolved tool effects before continuing.", state.ID)
	case "paused":
		return fmt.Sprintf("Task checkpoint saved (flow %s). Automatic continuation is paused.", state.ID)
	default:
		return fmt.Sprintf("Task hit max-steps budget. Checkpoint saved (flow %s) — will resume.", state.ID)
	}
}

var errStepBudget = errors.New("agent: exceeded max steps")

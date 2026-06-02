// Package agent — flow resume entrypoint. Called from the alarm
// dispatcher when an alarm whose prompt encodes a flow ID fires. The
// resume path:
//
//  1. Loads the persisted FlowState
//  2. Bumps Resumes (gated by MaxResumes — three strikes and stop)
//  3. Restores the agent's history from the checkpoint
//  4. Re-enters Stream with the original user input
//  5. On clean completion, Delete the flow record so it doesn't
//     keep showing up in `alarm_list`. On another timeout, the
//     next CheckpointFlow overwrites the same record.
//
// The resume contract is "complete this task from where it stopped"
// — no human in the loop. Errors are non-recoverable: a corrupt
// checkpoint, an exhausted flow, or a missing record all return
// without retrying. The alarm dispatcher logs and moves on.
package agent

import (
	"context"
	"fmt"
	"strings"
)

// FlowResumePrefix is the prompt prefix the alarm dispatcher looks
// for to recognise a resume alarm. Set when CheckpointFlow's caller
// schedules the alarm; checked when the alarm fires.
const FlowResumePrefix = "overkill:flow:resume:"

// FormatResumePrompt builds the alarm prompt that triggers resume.
// Use this everywhere the daemon constructs a resume alarm so the
// prefix stays in one place.
func FormatResumePrompt(flowID string) string {
	return FlowResumePrefix + flowID
}

// ExtractFlowID inspects a prompt and returns the embedded flow ID,
// or "" when this isn't a resume prompt. Lets the dispatcher cheaply
// branch on prompt shape without committing to a richer payload.
func ExtractFlowID(prompt string) string {
	prompt = strings.TrimSpace(prompt)
	if !strings.HasPrefix(prompt, FlowResumePrefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(prompt, FlowResumePrefix))
}

// ResumeFlow re-hydrates the agent from a persisted checkpoint and
// re-enters the streaming loop. The caller (typically the daemon's
// alarm dispatcher) provides an agent built fresh for this resume —
// we do NOT mutate the original agent that timed out, because it has
// already shut down with the TUI session.
//
// Returns the assembled response text and the final FlowState. On
// ErrFlowExhausted the agent is NOT invoked; the caller should
// surface "task too large, gave up after N resumes" instead.
func ResumeFlow(ctx context.Context, store FlowStore, flowID string, a *Agent) (string, *FlowState, error) {
	if store == nil {
		return "", nil, fmt.Errorf("flow resume: store not wired")
	}
	if a == nil {
		return "", nil, fmt.Errorf("flow resume: agent not provided")
	}

	state, err := MarkResumed(store, flowID)
	if err != nil {
		// ErrFlowExhausted or ErrFlowCorrupt — both terminal.
		return "", state, err
	}

	// Restore history before invoking Stream so the model has the
	// full context the original task left behind.
	a.RestoreHistory(state.History)

	stream, err := a.Stream(ctx, state.UserInput)
	if err != nil {
		return "", state, fmt.Errorf("flow resume: stream: %w", err)
	}

	var content strings.Builder
	for ev := range stream {
		switch ev.Type {
		case EventToken:
			content.WriteString(ev.Content)
		case EventDone:
			// Clean exit. If the resumed run ALSO hit max-steps, the
			// stream loop's checkpoint path already overwrote the
			// FlowState record. Don't delete in that case — let the
			// next resume pick it up.
			if ev.Result != nil && hasFlowCheckpointMeta(ev.Metadata) {
				return content.String(), state, nil
			}
			// Successful completion — clean up the record so it
			// doesn't show in `alarm_list` forever.
			_ = store.Delete(flowID)
			return content.String(), state, nil
		case EventError:
			// Stream-side error (e.g. provider failure). Leave the
			// record in place so a future manual retry has a starting
			// point.
			return content.String(), state, ev.Error
		}
	}
	// Channel closed without EventDone or EventError — shouldn't
	// happen but treat as success since the agent's intent was to
	// finish.
	_ = store.Delete(flowID)
	return content.String(), state, nil
}

// hasFlowCheckpointMeta reports whether a Done event carries a
// flow_checkpoint key — i.e. the resumed run itself timed out.
func hasFlowCheckpointMeta(meta map[string]interface{}) bool {
	if meta == nil {
		return false
	}
	_, ok := meta["flow_checkpoint"]
	return ok
}

// Package main — bridge between FlowState checkpoints and the
// alarm clock. When a flow checkpoint lands (either via the daemon's
// own agent or via the TUI's flow.checkpoint RPC), schedule a resume
// alarm using a stable per-flow ID so successive checkpoints replace
// the existing alarm rather than stacking them up.
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/automation"
)

const defaultResumeAfter = 5 * time.Minute

// scheduleResumeAlarm registers (or replaces) the resume alarm for a
// flow. The alarm ID is derived from the flow ID so re-checkpointing
// the same flow doesn't create a second alarm — the AlarmClock's Set
// rejects duplicates, so we Cancel + Set to keep the latest schedule.
func scheduleResumeAlarm(clock *automation.AlarmClock, flowID, resumeAfter string) {
	if clock == nil || flowID == "" {
		return
	}
	delay := defaultResumeAfter
	if resumeAfter != "" {
		if d, err := time.ParseDuration(resumeAfter); err == nil && d > 0 {
			delay = d
		}
	}

	alarmID := "resume-" + flowID
	// Cancel any prior alarm for this flow so the AlarmClock's
	// "duplicate ID" check doesn't reject the new schedule. Cancel
	// returns false on unknown IDs which is fine — we wanted it gone
	// either way.
	clock.Cancel(alarmID)

	err := clock.Set(&automation.Alarm{
		ID:     alarmID,
		Name:   "resume " + flowID,
		FireAt: time.Now().Add(delay),
		Prompt: agent.FormatResumePrompt(flowID),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "flow resume: schedule alarm: %v\n", err)
	}
}

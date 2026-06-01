package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Sahaj-Tech-ltd/overkill/internal/automation"
	"github.com/Sahaj-Tech-ltd/overkill/internal/journal"
)

// taskCompletionAlertSink returns a TerminalSink that writes one
// AlertTaskCompleted record per terminal transition. The gateway
// hub reads these out of the shared alert store and forwards to
// the user's bound channels — keeping the daemon process and the
// gateway process loosely coupled via files instead of an RPC.
//
// Best-effort:
//   - Missing HOME → no-op.
//   - Failed AlertStore write → swallowed; the in-process ledger
//     still reflects the transition so the user can see it from
//     `overkill daemon status` and `journal_search` next session.
//
// The sink is fired by the ledger OUTSIDE its own lock, so a slow
// disk doesn't serialize background-task updates.
func taskCompletionAlertSink() automation.TerminalSink {
	home, err := os.UserHomeDir()
	if err != nil {
		return func(automation.LedgerTask) {}
	}
	alertDir := filepath.Join(home, ".overkill", "alerts")
	if err := os.MkdirAll(alertDir, 0o755); err != nil {
		return func(automation.LedgerTask) {}
	}
	store := journal.NewAlertStore(alertDir)
	_ = store.Load()
	return func(t automation.LedgerTask) {
		msg := formatTaskCompletionMessage(t)
		_ = store.Create(journal.AlertTaskCompleted, msg, "" /* daemon-wide, no session id */)
		// §4.19 SSE broadcast — live subscribers see completion in
		// real time instead of waiting for the next poll. Best-
		// effort and non-blocking; nil dashboard (server not
		// running) is a no-op.
		if daemonDashboard != nil {
			alert := &journal.Alert{
				Type:      journal.AlertTaskCompleted,
				Message:   msg,
				SessionID: "",
			}
			daemonDashboard.BroadcastAlert(alert)
		}
	}
}

// formatTaskCompletionMessage renders the one-line summary the user
// sees in their channels. Terse on purpose — push notifications
// have a tight character budget.
func formatTaskCompletionMessage(t automation.LedgerTask) string {
	label := t.Source
	if label == "" {
		label = "task"
	}
	if t.Name != "" {
		label = label + "/" + t.Name
	}
	switch t.State {
	case automation.TaskCompleted:
		if t.Result != "" {
			return fmt.Sprintf("✓ %s completed: %s", label, firstLineSummary(t.Result))
		}
		return fmt.Sprintf("✓ %s completed", label)
	case automation.TaskFailed:
		if t.Error != "" {
			return fmt.Sprintf("✗ %s failed: %s", label, firstLineSummary(t.Error))
		}
		return fmt.Sprintf("✗ %s failed", label)
	case automation.TaskCancelled:
		return fmt.Sprintf("⊘ %s cancelled", label)
	case automation.TaskTimedOut:
		return fmt.Sprintf("⏱ %s timed out", label)
	case automation.TaskLost:
		return fmt.Sprintf("⚠ %s lost (no heartbeat)", label)
	default:
		return fmt.Sprintf("%s now %s", label, t.State)
	}
}

// firstLineSummary truncates long result/error strings so a single
// notification doesn't dominate a chat client. Keeps the first
// non-empty line, capped at 160 chars.
func firstLineSummary(s string) string {
	for _, line := range splitLines(s) {
		if line != "" {
			if len(line) > 160 {
				return line[:160] + "…"
			}
			return line
		}
	}
	return ""
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

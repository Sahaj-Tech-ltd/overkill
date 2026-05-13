package agent

import (
	"context"
	"strings"
	"time"
)

// preCompactCheck — master plan §4.4 pre-compaction checkpoint.
//
// When the agent is approaching the auto-compact threshold AND the user
// just queued something that looks expensive, we compact NOW instead of
// during the upcoming big task. Two wins:
//  1. The big task gets a fresh window (no mid-turn auto-compact stall).
//  2. The journal/memory archives the recent context before it's
//     summarised — saving signal that would otherwise be lost.
//
// Heuristic for "large incoming task":
//   - >= 800 characters of input, OR
//   - contains a fenced code block, OR
//   - leads with a multi-step verb ("refactor", "implement", "design",
//     "build", "rewrite", "audit", "migrate") and is at least 80 chars.
//
// Heuristic for "approaching compaction":
//   - utilization in [0.45, soft threshold]
//   - haven't already pre-compacted this session within the last 60s
//
// Returns true when a pre-compaction was performed. Best-effort —
// failures emit an event but don't block Run().
const preCompactWindowLow = 0.45

var preCompactVerbs = []string{
	"refactor", "implement", "design", "build", "rewrite",
	"audit", "migrate", "rearchitect", "redesign",
}

func (a *Agent) preCompactCheck(ctx context.Context, userInput string) bool {
	// Cheap gates first.
	if a.budgetEstimator == nil {
		return false
	}
	useC := a.useCompactor.Load()
	a.mu.RLock()
	c := a.compactor
	last := a.lastPreCompactAt
	a.mu.RUnlock()
	if !useC || c == nil {
		return false
	}
	// Throttle: at most one pre-compact per 60s. The auto-compact at
	// τ_soft handles the rest.
	if !last.IsZero() && time.Since(last) < 60*time.Second {
		return false
	}

	report := a.BudgetReport()
	if report == nil {
		return false
	}
	// Already past the soft trigger — auto-compact owns this case.
	if report.ShouldCompact {
		return false
	}
	// Below the warning band — too early.
	if report.Utilization < preCompactWindowLow {
		return false
	}

	if !looksLikeLargeTask(userInput) {
		return false
	}

	a.emit("pre_compact_triggered", map[string]any{
		"utilization": report.Utilization,
		"reason":      "approaching threshold with large incoming task",
		"session_id":  a.sessionID,
	})

	// Acquire the in-flight guard so the budget-driven auto-compact
	// doesn't double-fire on the same window. Use the same atomic the
	// 50%-trigger uses.
	if !a.compactionInFlight.CompareAndSwap(false, true) {
		return false
	}
	defer a.compactionInFlight.Store(false)

	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	if _, err := a.Compact(cctx); err != nil {
		a.emit("pre_compact_failed", map[string]any{
			"error":      err.Error(),
			"session_id": a.sessionID,
		})
		return false
	}

	a.mu.Lock()
	a.lastPreCompactAt = time.Now()
	a.mu.Unlock()
	a.emit("pre_compact_done", map[string]any{
		"session_id": a.sessionID,
	})
	return true
}

// looksLikeLargeTask is the cheap classifier for the "incoming work is
// expensive" gate. Conservative — false positives waste one compaction
// (cheap); false negatives let a 50%-utilization session hit auto-
// compact mid-task (more disruptive).
func looksLikeLargeTask(input string) bool {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return false
	}
	if len(trimmed) >= 800 {
		return true
	}
	if strings.Contains(trimmed, "```") {
		return true
	}
	lower := strings.ToLower(trimmed)
	if len(trimmed) >= 80 {
		first := firstWordLower(lower)
		for _, v := range preCompactVerbs {
			if first == v {
				return true
			}
		}
	}
	return false
}

func firstWordLower(s string) string {
	for i, r := range s {
		if r == ' ' || r == '\t' || r == '\n' {
			return s[:i]
		}
	}
	return s
}

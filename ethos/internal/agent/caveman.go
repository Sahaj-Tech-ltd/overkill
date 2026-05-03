// Package agent — Caveman Mode (master plan §4.4).
//
// As the token budget approaches its cap, escalate the system prompt with
// a directive to be progressively terser. Three tiers — chatty (default),
// curt (50% budget), caveman (80%+) — so the agent voluntarily compresses
// its own output before it forces a hard compaction.
//
// The mutation is purely additive: we append a directive to the existing
// system prompt rather than replace it. Callers with no budget estimator
// get the original prompt unchanged.
package agent

// CavemanLevel reports the current bluntness tier.
type CavemanLevel int

const (
	CavemanOff   CavemanLevel = iota // no mutation
	CavemanCurt                      // utilization >= 0.50
	CavemanBlunt                     // utilization >= 0.65
	CavemanFull                      // utilization >= 0.80
)

// LevelFromUtilization maps a utilization fraction to a CavemanLevel.
func LevelFromUtilization(u float64) CavemanLevel {
	switch {
	case u >= 0.80:
		return CavemanFull
	case u >= 0.65:
		return CavemanBlunt
	case u >= 0.50:
		return CavemanCurt
	default:
		return CavemanOff
	}
}

// String returns a short label.
func (l CavemanLevel) String() string {
	switch l {
	case CavemanCurt:
		return "curt"
	case CavemanBlunt:
		return "blunt"
	case CavemanFull:
		return "caveman"
	default:
		return "off"
	}
}

// Directive is the line appended to the system prompt for a given level.
// Returns empty for CavemanOff so callers can skip the append.
func (l CavemanLevel) Directive() string {
	switch l {
	case CavemanCurt:
		return "TOKEN BUDGET — be concise. Skip preamble, restate nothing, prefer code blocks over prose."
	case CavemanBlunt:
		return "TOKEN BUDGET CRITICAL — terse only. One-sentence answers. Drop pleasantries. Show diff/code, not narration."
	case CavemanFull:
		return "TOKEN BUDGET EXHAUSTED. Caveman speak. Tools direct. No words wasted. Diff only. No 'here is' or 'sure'. Bullet > sentence."
	default:
		return ""
	}
}

// applyCaveman appends the directive for the agent's current utilization
// onto the system prompt. Returns the original prompt when no estimator is
// wired or utilization is below the first threshold.
func (a *Agent) applyCaveman(prompt string) string {
	if a == nil || a.budgetEstimator == nil {
		return prompt
	}
	rep := a.BudgetReport()
	if rep == nil || rep.MaxTokens <= 0 {
		return prompt
	}
	lvl := LevelFromUtilization(rep.Utilization)
	dir := lvl.Directive()
	if dir == "" {
		return prompt
	}
	if prompt == "" {
		return dir
	}
	return prompt + "\n\n" + dir
}

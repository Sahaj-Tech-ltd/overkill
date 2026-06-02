package agent

import (
	"context"
	"strings"
	"time"
)

// taskTimeoutFor returns the wall-clock budget for a single Run() based
// on a cheap complexity classification of the user's input (master plan
// §7.1). Auto-scaled bands:
//
//	simple    (≤ 0.30) → 60s   — "what time is it", "lint this file"
//	moderate  (≤ 0.60) → 5m    — typical refactor / small feature
//	complex   (≤ 0.85) → 15m   — multi-step, multi-file work
//	critical  ( > 0.85) → 30m  — architectural changes, attachments
//
// The classifier here is intentionally separate from internal/routing's
// model-routing classifier — same INPUT signals, different DOWNSTREAM
// use. Keeping the agent free of internal/routing means tests don't
// have to wire a full router just to verify the timeout math.
//
// Returns a default mid-band timeout when input is empty.
func taskTimeoutFor(input string, historyLen int) time.Duration {
	s := complexityScore(input, historyLen)
	switch {
	case s <= 0.30:
		return 60 * time.Second
	case s <= 0.60:
		return 5 * time.Minute
	case s <= 0.85:
		return 15 * time.Minute
	default:
		return 30 * time.Minute
	}
}

// complexityScore is a tiny mirror of routing.Classifier with the same
// signals. Returns 0..1. Public-ish so tests can pin behaviour without
// pulling internal/routing.
func complexityScore(input string, historyLen int) float64 {
	if strings.TrimSpace(input) == "" {
		return 0.45 // unknown → assume moderate
	}
	s := 0.0
	lower := strings.ToLower(input)

	if len(input) > 800 {
		s += 0.35
	} else if len(input) > 200 {
		s += 0.15
	}

	switch strings.Count(input, "```") / 2 {
	case 0:
		// no code blocks → no bonus
	case 1:
		s += 0.20
	default:
		s += 0.40
	}

	if historyLen > 10 {
		s += 0.10
	}

	// File / path attachment hints — the routing classifier uses a
	// hard 1.0 gate when actual attachments are present; we don't
	// have that signal here, so substring-detect typical patterns.
	if strings.Contains(input, "@") {
		s += 0.10
	}

	// Verb gates — same shortlist used by the pre-compact heuristic.
	first := firstWordLower(lower)
	for _, v := range preCompactVerbs {
		if first == v {
			s += 0.20
			break
		}
	}
	// Simple-intent prefixes reduce the score.
	for _, p := range []string{"explain ", "what ", "how ", "why ", "list "} {
		if strings.HasPrefix(lower, p) {
			s -= 0.10
			break
		}
	}

	if s < 0 {
		s = 0
	}
	if s > 1 {
		s = 1
	}
	return s
}

// withTaskTimeout returns a derived context bounded by the auto-scaled
// task budget. Used by Run() so a single user message can't burn
// arbitrary wall-clock on the provider. Caller MUST defer cancel().
func withTaskTimeout(parent context.Context, input string, historyLen int) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, taskTimeoutFor(input, historyLen))
}

// Package monitor — behavioral session monitor (paper #48 design
// input, "Wall 4" in §6.5 ergonomics).
//
// Different shape from §6.5 Wall 1 (Red Team auto-trigger): Red Team
// fires per-task on critical paths, asking "is this PR adversarially
// safe?". The monitor scans HISTORICAL session entries, looking for
// behavior categories OpenAI's monitoring paper catalogued —
// circumventing restrictions, deception, concealing uncertainty,
// reward hacking, unauthorized data transfer. The point is detection
// of patterns that escaped per-call gates, not pre-commit review.
//
// Design choices:
//
//   - Pure-Go heuristic detectors, no LLM call. Cheap to run on a
//     cron tick across a whole session. A future iteration can call
//     a cheap-tier model to confirm before alerting, but the
//     heuristic baseline catches the obvious cases.
//   - Each category has its own detector with conservative thresholds
//     — we'd rather miss a subtle case than spam alerts.
//   - Findings carry the category, the entry that triggered, and a
//     human-readable reason. The agent + user can drill in via the
//     journal entry ID.
package monitor

import (
	"strings"

	"github.com/Sahaj-Tech-ltd/overkill/internal/journal"
)

// Category names the behavior bucket. Mapped from the OpenAI paper's
// taxonomy — we ship detectors for the ones we have signal for.
type Category string

const (
	// CategoryCircumvention covers encoded-bypass attempts (base64
	// pipelines, hex-decode shapes) — we already block at runtime
	// via internal/security/decode.go; the monitor catches anything
	// that landed in the journal as a tool_call despite the gate.
	CategoryCircumvention Category = "circumventing_restrictions"

	// CategoryDeception covers assistant claims of success that
	// don't have a supporting tool result. "tests pass" without a
	// test-runner tool call; "fixed the bug" without a verify step.
	CategoryDeception Category = "deception"

	// CategoryConcealingUncertainty covers high-confidence claims
	// without supporting evidence — overlap with halluscan G3 but
	// at session-scope rather than per-turn.
	CategoryConcealingUncertainty Category = "concealing_uncertainty"

	// CategoryRewardHacking covers test-without-code edits that
	// escaped the per-turn auditor (e.g. across two separate turns
	// that together hide the pattern).
	CategoryRewardHacking Category = "reward_hacking"

	// CategoryDataTransfer covers shell calls or web requests
	// pointing at unknown external endpoints — possible
	// exfiltration. We flag conservatively (allow-list of common
	// public endpoints; everything else with curl/wget is a
	// candidate).
	CategoryDataTransfer Category = "unauthorized_data_transfer"
)

// Finding is one monitor hit. EntryID points back at the journal
// entry that triggered, so the user can drill in.
type Finding struct {
	Category  Category
	EntryID   string
	SessionID string
	Reason    string
	// Confidence is the heuristic's own estimate (0..1). Below 0.6
	// is "worth a look", above 0.85 is "almost certainly worth
	// surfacing". Caller decides the threshold for emitting alerts.
	Confidence float64
}

// Scan walks the entries (most-recent-first or oldest-first, doesn't
// matter for correctness) and runs every detector. Returns the
// aggregate findings.
//
// Detectors are deliberately separable functions so a future
// LLM-confirmation pass can wrap any of them without touching the
// dispatch loop.
func Scan(entries []journal.Entry) []Finding {
	var out []Finding
	out = append(out, detectCircumvention(entries)...)
	out = append(out, detectDeception(entries)...)
	out = append(out, detectConcealingUncertainty(entries)...)
	out = append(out, detectRewardHacking(entries)...)
	out = append(out, detectDataTransfer(entries)...)
	return out
}

// FormatAlert renders findings as a human-readable summary the
// monitor surfaces via the journal alerts.md file. Empty when no
// findings — caller skips emission.
func FormatAlert(findings []Finding) string {
	if len(findings) == 0 {
		return ""
	}
	// Group by category so the user reads "3 deception, 1 data
	// transfer" instead of an interleaved blob.
	byCategory := map[Category][]Finding{}
	for _, f := range findings {
		byCategory[f.Category] = append(byCategory[f.Category], f)
	}
	var b strings.Builder
	b.WriteString("[monitor] behavioral scan found ")
	if len(findings) == 1 {
		b.WriteString("1 issue:\n\n")
	} else {
		b.WriteString(itoa(len(findings)))
		b.WriteString(" issues:\n\n")
	}
	// Stable ordering: deterministic over the categories we know.
	for _, cat := range []Category{
		CategoryCircumvention,
		CategoryDeception,
		CategoryConcealingUncertainty,
		CategoryRewardHacking,
		CategoryDataTransfer,
	} {
		hits := byCategory[cat]
		if len(hits) == 0 {
			continue
		}
		b.WriteString("  ")
		b.WriteString(string(cat))
		b.WriteString(" (")
		b.WriteString(itoa(len(hits)))
		b.WriteString("):\n")
		for _, h := range hits {
			b.WriteString("    - ")
			b.WriteString(h.Reason)
			if h.EntryID != "" {
				b.WriteString(" [entry ")
				b.WriteString(h.EntryID)
				b.WriteString("]")
			}
			b.WriteByte('\n')
		}
	}
	b.WriteString("\nReview via `overkill journal show <id>` or dismiss with `overkill monitor ack`.\n")
	return b.String()
}

// itoa is the same small no-dep integer-to-string helper we use
// elsewhere — fmt.Sprintf would work but pulls fmt into a package
// that otherwise needs none.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

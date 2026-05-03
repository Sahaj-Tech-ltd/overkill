// Package diagnostic — 10-tier feedback loop ladder (master plan §4.13).
//
// When a fix attempt fails, escalate the verification depth one tier at a
// time. Each tier is more expensive than the last; we only climb when the
// previous tier passed but the bug recurred (a stronger signal that the
// last tier didn't actually exercise the broken behavior).
//
// The ladder is deliberately ordered cheapest → most expensive:
//
//   1. compile      — does it build?
//   2. unit-test    — does the failing test now pass?
//   3. broader-test — do the surrounding tests pass?
//   4. lint         — does static analysis flag anything?
//   5. cli          — does invoking via CLI reproduce the symptom?
//   6. curl         — does an end-to-end network probe pass?
//   7. headless-browser — does headless browser smoke pass?
//   8. property     — does property-based / fuzz testing find a counterexample?
//   9. bisect       — git bisect against prior known-good?
//  10. hitl-bash    — surface to the human; ask them to run a bash check.
//
// The ladder is data — callers (tools, slash command, the agent itself)
// pick a starting tier and climb. We don't execute the commands here;
// callers map a Tier to the right tool/CLI invocation for their context.
package diagnostic

import (
	"errors"
	"fmt"
	"strings"
)

// Tier is one step on the verification ladder.
type Tier int

const (
	TierCompile Tier = iota + 1
	TierUnitTest
	TierBroaderTest
	TierLint
	TierCLI
	TierCurl
	TierHeadlessBrowser
	TierProperty
	TierBisect
	TierHITLBash
)

// AllTiers returns the ladder in order.
func AllTiers() []Tier {
	return []Tier{
		TierCompile, TierUnitTest, TierBroaderTest, TierLint, TierCLI,
		TierCurl, TierHeadlessBrowser, TierProperty, TierBisect, TierHITLBash,
	}
}

// Name is a human-readable label for the tier.
func (t Tier) Name() string {
	switch t {
	case TierCompile:
		return "compile"
	case TierUnitTest:
		return "unit-test"
	case TierBroaderTest:
		return "broader-test"
	case TierLint:
		return "lint"
	case TierCLI:
		return "cli"
	case TierCurl:
		return "curl"
	case TierHeadlessBrowser:
		return "headless-browser"
	case TierProperty:
		return "property"
	case TierBisect:
		return "bisect"
	case TierHITLBash:
		return "hitl-bash"
	default:
		return fmt.Sprintf("tier-%d", int(t))
	}
}

// Description explains what the tier verifies.
func (t Tier) Description() string {
	switch t {
	case TierCompile:
		return "Does the code compile / type-check cleanly?"
	case TierUnitTest:
		return "Does the failing unit test now pass?"
	case TierBroaderTest:
		return "Does the surrounding test package still pass?"
	case TierLint:
		return "Does static analysis (vet/eslint/clippy) flag anything new?"
	case TierCLI:
		return "Does invoking the affected entry point via CLI reproduce/clear the symptom?"
	case TierCurl:
		return "Does an end-to-end network probe (curl) hit the right behavior?"
	case TierHeadlessBrowser:
		return "Does a headless-browser smoke test hit the right UI behavior?"
	case TierProperty:
		return "Does a property-based / fuzz pass find a counterexample?"
	case TierBisect:
		return "Does git bisect against a known-good revision narrow the regression?"
	case TierHITLBash:
		return "Surface to the human: ask them to run a custom bash check."
	default:
		return ""
	}
}

// SuggestedCommand returns a starter shell command for the tier given a
// language ecosystem. Empty when no obvious default applies.
func (t Tier) SuggestedCommand(lang string) string {
	lang = strings.ToLower(lang)
	switch t {
	case TierCompile:
		switch lang {
		case "go":
			return "go build ./..."
		case "rust":
			return "cargo check"
		case "typescript", "ts":
			return "tsc --noEmit"
		case "python", "py":
			return "python -m compileall ."
		}
	case TierUnitTest:
		switch lang {
		case "go":
			return "go test ./...  -run TARGET"
		case "rust":
			return "cargo test TARGET"
		case "typescript", "ts":
			return "vitest run -t TARGET"
		case "python", "py":
			return "pytest -k TARGET"
		}
	case TierBroaderTest:
		switch lang {
		case "go":
			return "go test ./..."
		case "rust":
			return "cargo test"
		case "typescript", "ts":
			return "vitest run"
		case "python", "py":
			return "pytest"
		}
	case TierLint:
		switch lang {
		case "go":
			return "go vet ./..."
		case "rust":
			return "cargo clippy -- -D warnings"
		case "typescript", "ts":
			return "eslint ."
		case "python", "py":
			return "ruff check ."
		}
	case TierCurl:
		return "curl -sS -o /dev/null -w '%{http_code}\\n' http://localhost:PORT/PATH"
	case TierBisect:
		return "git bisect start && git bisect bad HEAD && git bisect good GOOD_SHA"
	case TierHITLBash:
		return "# escalate to user — write a clear repro question"
	}
	return ""
}

// Ladder tracks progress up the tiers. Climb() returns the next tier to try,
// or io.EOF-style sentinel when exhausted.
type Ladder struct {
	current int // index into AllTiers
}

// NewLadder creates a fresh ladder pointing at TierCompile.
func NewLadder() *Ladder { return &Ladder{} }

// FromTier creates a ladder starting at the given tier (skip cheaper checks).
func FromTier(t Tier) *Ladder {
	for i, tier := range AllTiers() {
		if tier == t {
			return &Ladder{current: i}
		}
	}
	return NewLadder()
}

// Current returns the tier the ladder is currently sitting on.
func (l *Ladder) Current() Tier {
	tiers := AllTiers()
	if l.current >= len(tiers) {
		return tiers[len(tiers)-1]
	}
	return tiers[l.current]
}

// Climb advances to the next tier. Returns ErrLadderExhausted when there
// are no more rungs.
func (l *Ladder) Climb() (Tier, error) {
	tiers := AllTiers()
	l.current++
	if l.current >= len(tiers) {
		l.current = len(tiers) - 1
		return tiers[l.current], ErrLadderExhausted
	}
	return tiers[l.current], nil
}

// Reset returns the ladder to its bottom rung.
func (l *Ladder) Reset() { l.current = 0 }

// ErrLadderExhausted indicates Climb was called past the top tier.
var ErrLadderExhausted = errors.New("diagnostic: ladder exhausted (escalate to human)")

package agent

import (
	"strings"
	"sync"

	"github.com/Sahaj-Tech-ltd/overkill/internal/diagnostic"
)

// diagnosticEscalator owns one Ladder per error class so a recurring failure
// climbs to a stronger verification tier each time it surfaces. The
// per-class split prevents a flaky network test from advancing the compile
// ladder, and vice versa.
//
// Master plan §4.13: "Build a feedback loop FIRST. The 10-tier escalation
// is the skill. Everything else is mechanical." We don't EXECUTE the tier's
// suggested check — that's the user's call. We surface it via the
// diagnostic_suggestion event so the recovery report tells the user "next
// time, run this stronger check before declaring victory."
type diagnosticEscalator struct {
	mu      sync.Mutex
	ladders map[string]*diagnostic.Ladder
	// Bound the map so a pathological loop emitting unique error classes
	// can't grow it unboundedly. Past the cap we re-classify into a single
	// "overflow" bucket.
	cap int
}

func newDiagnosticEscalator() *diagnosticEscalator {
	return &diagnosticEscalator{
		ladders: make(map[string]*diagnostic.Ladder),
		cap:     64,
	}
}

// diagnosticSuggestion is the payload emitted on each agent error.
type diagnosticSuggestion struct {
	Class       string
	Tier        int
	Name        string
	Description string
	Command     string
	Exhausted   bool
}

// suggest returns the next tier to recommend for this error class and
// advances the per-class ladder. First sighting starts at the class's
// natural tier (compile errors at TierCompile, runtime at TierUnitTest,
// etc.); subsequent sightings climb one rung.
func (e *diagnosticEscalator) suggest(errMsg string) diagnosticSuggestion {
	cls := classifyForLadder(errMsg)

	e.mu.Lock()
	defer e.mu.Unlock()

	l, ok := e.ladders[cls]
	if !ok {
		if len(e.ladders) >= e.cap {
			cls = "overflow"
			l = e.ladders[cls]
		}
		if l == nil {
			l = diagnostic.FromTier(initialTierFor(cls))
			e.ladders[cls] = l
		}
	} else {
		// Repeat sighting — climb. We don't reset; the ladder is monotonic
		// per error class until the session ends.
		_, _ = l.Climb()
	}

	t := l.Current()
	exhausted := t == diagnostic.TierHITLBash
	return diagnosticSuggestion{
		Class:       cls,
		Tier:        int(t),
		Name:        t.Name(),
		Description: t.Description(),
		Command:     t.SuggestedCommand("go"),
		Exhausted:   exhausted,
	}
}

// classifyForLadder maps an error message to a coarse class. Mirrors
// diagnostic.Analyzer.ClassifyError but without needing an Analyzer
// instance (which carries an LLM provider we don't need here).
func classifyForLadder(errMsg string) string {
	lower := strings.ToLower(errMsg)
	switch {
	case containsAnyDiag(lower, "--- fail", "test failed", "fail\t"):
		return "test"
	case containsAnyDiag(lower,
		"panic:", "nil pointer", "index out of range",
		"sigsegv", "segmentation fault", "fatal error:",
		"goroutine ", "deadlock"):
		return "runtime"
	case containsAnyDiag(lower,
		"compile error", "syntax error", "undefined:",
		"cannot find", "not used", "imported and not used",
		"expected ", "unexpected ", "missing return"):
		return "compile"
	case containsAnyDiag(lower, "lint", "style", "format", "vet "):
		return "lint"
	case containsAnyDiag(lower,
		"connection refused", "no such host", "i/o timeout",
		"tcp ", "tls", "certificate"):
		return "network"
	}
	return "unknown"
}

func containsAnyDiag(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// initialTierFor picks the natural starting rung for each class. Compile
// errors start at TierCompile (cheapest, will catch most), runtime starts
// at TierUnitTest (compile already passed if we got here), etc.
func initialTierFor(cls string) diagnostic.Tier {
	switch cls {
	case "compile":
		return diagnostic.TierCompile
	case "lint":
		return diagnostic.TierLint
	case "test":
		return diagnostic.TierUnitTest
	case "runtime":
		return diagnostic.TierUnitTest
	case "network":
		return diagnostic.TierCurl
	default:
		return diagnostic.TierCompile
	}
}

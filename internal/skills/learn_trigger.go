// Package skills — self-learning trigger (master plan §6.2).
//
// LearnTrigger watches for successful turns on a problem class and emits a
// "save as skill?" suggestion once N successes accumulate. The suggestion
// is structured (event + suggested name + rough description); the agent
// (or user) decides whether to invoke skill_extract.
//
// Per-class state lives in memory; persistence is the caller's job — most
// callers just want the in-session signal.
package skills

import (
	"strings"
	"sync"
	"time"
)

// SuggestThreshold is the default success count that triggers a suggestion.
const SuggestThreshold = 3

// Suggestion is what the trigger emits when the threshold is hit.
type Suggestion struct {
	Class       string    `json:"class"`     // problem class key (e.g. "test-flaky-fix")
	Successes   int       `json:"successes"` // count at time of suggestion
	SuggestedAt time.Time `json:"suggested_at"`
	SkillName   string    `json:"skill_name"` // sanitized class → skill folder name
	Hint        string    `json:"hint"`       // human-readable nudge
}

// LearnTrigger tracks per-class success counts and fires a callback once
// per class when SuggestThreshold is crossed. Subsequent successes on the
// same class do not re-fire (one suggestion per class per process — user
// already saw the offer).
type LearnTrigger struct {
	mu        sync.Mutex
	threshold int
	counts    map[string]int
	suggested map[string]bool
	onSuggest func(Suggestion)
}

// NewLearnTrigger wires a callback. Threshold <= 0 falls back to default.
func NewLearnTrigger(threshold int, onSuggest func(Suggestion)) *LearnTrigger {
	if threshold <= 0 {
		threshold = SuggestThreshold
	}
	return &LearnTrigger{
		threshold: threshold,
		counts:    map[string]int{},
		suggested: map[string]bool{},
		onSuggest: onSuggest,
	}
}

// RecordSuccess increments the class counter and fires onSuggest if the
// threshold was just crossed. Returns true when a suggestion fired.
func (t *LearnTrigger) RecordSuccess(class string) bool {
	class = strings.TrimSpace(class)
	if class == "" {
		return false
	}
	t.mu.Lock()
	t.counts[class]++
	count := t.counts[class]
	already := t.suggested[class]
	threshold := t.threshold
	cb := t.onSuggest
	if count >= threshold && !already {
		t.suggested[class] = true
	}
	t.mu.Unlock()

	if count >= threshold && !already && cb != nil {
		cb(Suggestion{
			Class:       class,
			Successes:   count,
			SuggestedAt: time.Now().UTC(),
			SkillName:   sanitizeName(class),
			Hint:        "You've solved this " + ntimes(count) + ". Want me to save it as a skill?",
		})
		return true
	}
	return false
}

// Reset zeroes a class so the next threshold-crossing fires again. Useful
// when the user dismisses a suggestion and you want to nudge again later.
func (t *LearnTrigger) Reset(class string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.counts, class)
	delete(t.suggested, class)
}

// Snapshot returns a copy of the per-class success counts. Useful for
// /skills introspection.
func (t *LearnTrigger) Snapshot() map[string]int {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make(map[string]int, len(t.counts))
	for k, v := range t.counts {
		out[k] = v
	}
	return out
}

func ntimes(n int) string {
	switch n {
	case 1:
		return "once"
	case 2:
		return "twice"
	case 3:
		return "3 times"
	default:
		return strings.Repeat("·", 0) + // keep gofmt happy
			itoa(n) + " times"
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

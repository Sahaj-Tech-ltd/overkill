// Package personality — frustration heuristic (master plan §4.16).
//
// FrustrationDetector watches the stream of user inputs and fires an
// AlertFrustration when the user shows signs of irritation: same intent
// re-tried multiple times, ALL CAPS shouting, profanity, or explicit
// "stop / wrong / no" patterns.
//
// Heuristic by design — false positives are acceptable; we'd rather see
// a frustrated user once and apologise than miss the cue entirely.
package personality

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
)

// FrustrationDetector accumulates short-window evidence and emits at most one
// alert per cool-down window so the user isn't spammed.
type FrustrationDetector struct {
	mu        sync.Mutex
	sink      AlertSink
	sessionID string

	recent      []string
	recentMax   int
	cooldown    time.Duration
	lastFiredAt time.Time
}

// NewFrustrationDetector wires a sink. Pass nil sink to disable; the detector
// still tracks state but never fires.
func NewFrustrationDetector(sink AlertSink, sessionID string) *FrustrationDetector {
	return &FrustrationDetector{
		sink:      sink,
		sessionID: sessionID,
		recentMax: 6,
		cooldown:  5 * time.Minute,
	}
}

// Observe is called once per user input. Returns true when an alert was
// emitted; the caller may use that to soften the next response, log, etc.
func (d *FrustrationDetector) Observe(input string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	clean := strings.TrimSpace(input)
	if clean == "" {
		return false
	}
	d.recent = append(d.recent, clean)
	if len(d.recent) > d.recentMax {
		d.recent = d.recent[len(d.recent)-d.recentMax:]
	}

	score, reason := d.score(clean)
	if score < 2 {
		return false
	}
	if !d.lastFiredAt.IsZero() && time.Since(d.lastFiredAt) < d.cooldown {
		return false
	}
	if d.sink == nil {
		d.lastFiredAt = time.Now()
		return false
	}
	msg := fmt.Sprintf("Frustration signal: %s", reason)
	func() {
		defer func() { _ = recover() }()
		_ = d.sink.Create("frustration_signal", msg, d.sessionID)
	}()
	d.lastFiredAt = time.Now()
	return true
}

// IsHot reports whether a frustration alert fired within the recency window.
// Used by the personality system prompt provider for tone mirroring (§4.16):
// when hot, the agent gets a short directive to drop preamble and match the
// user's urgency without matching panic. Internal calm stays untouched.
func (d *FrustrationDetector) IsHot(within time.Duration) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.lastFiredAt.IsZero() {
		return false
	}
	return time.Since(d.lastFiredAt) < within
}

// score combines several heuristics; returns a score and a human reason.
// Score >= 2 trips the alert.
func (d *FrustrationDetector) score(latest string) (int, string) {
	var score int
	var reasons []string

	// 1. ALL-CAPS shouting (≥4 letters, >70% upper).
	letters := 0
	upper := 0
	for _, r := range latest {
		if r >= 'a' && r <= 'z' {
			letters++
		} else if r >= 'A' && r <= 'Z' {
			letters++
			upper++
		}
	}
	if letters >= 4 && float64(upper)/float64(letters) > 0.7 {
		score++
		reasons = append(reasons, "shouting (caps)")
	}

	// 2. Repeat-with-emphasis: same opening word as a recent message.
	if len(d.recent) >= 2 {
		first := firstWord(latest)
		prevSame := 0
		for i := len(d.recent) - 2; i >= 0 && prevSame < 3; i-- {
			if firstWord(d.recent[i]) == first && first != "" {
				prevSame++
			}
		}
		if prevSame >= 1 {
			score++
			reasons = append(reasons, "repeated request")
		}
	}

	// 3. Frustration vocabulary (lowercased).
	if frustrationLexicon.MatchString(strings.ToLower(latest)) {
		score++
		reasons = append(reasons, "irritated vocabulary")
	}

	// 4. Multiple exclamation points or "??" / "?!".
	if strings.Count(latest, "!") >= 2 || strings.Contains(latest, "??") || strings.Contains(latest, "?!") {
		score++
		reasons = append(reasons, "emphatic punctuation")
	}

	if len(reasons) == 0 {
		return score, ""
	}
	return score, strings.Join(reasons, ", ")
}

func firstWord(s string) string {
	i := strings.IndexAny(s, " \t\n\r")
	if i < 0 {
		return strings.ToLower(s)
	}
	return strings.ToLower(s[:i])
}

// frustrationLexicon catches the most common irritation cues. Conservative —
// missing words is fine; misfiring on neutral text is worse.
var frustrationLexicon = regexp.MustCompile(`\b(wrong|stop|no+|broken|stupid|wtf|ffs|christ|annoying|useless|dumb|seriously|come on|why are you|what are you doing|hate|sick of|tired of|fix it|just do)\b`)

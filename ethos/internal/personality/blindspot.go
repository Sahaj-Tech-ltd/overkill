package personality

import (
	"fmt"
	"strings"
	"sync"
)

type BlindSpotEntry struct {
	Type    string
	Content string
}

// BlindSpotDetector tracks repeated user task-types and surfaces a
// rate-limited heads-up once a pattern is undeniable. All mutators
// + Check() take the mutex; concurrent access from input observers
// (per-turn Observe) and the personality provider (per-turn
// NextWarning) is now race-safe.
type BlindSpotDetector struct {
	mu         sync.Mutex
	patterns   map[string]int
	Threshold  int
	MaxAlerts  int
	alerted    map[string]bool
	alertCount int
	sink       AlertSink
	sessionID  string
}

// SetAlertSink wires a sink that receives a pattern_detected alert when
// Check() trips its threshold. Pass nil to disable.
func (bsd *BlindSpotDetector) SetAlertSink(s AlertSink, sessionID string) {
	bsd.mu.Lock()
	defer bsd.mu.Unlock()
	bsd.sink = s
	bsd.sessionID = sessionID
}

func NewBlindSpotDetector() *BlindSpotDetector {
	return &BlindSpotDetector{
		patterns:  make(map[string]int),
		Threshold: 4,
		MaxAlerts: 1,
		alerted:   make(map[string]bool),
	}
}

func (bsd *BlindSpotDetector) Observe(taskType string) {
	bsd.mu.Lock()
	defer bsd.mu.Unlock()
	bsd.patterns[taskType]++
}

func (bsd *BlindSpotDetector) Check() (string, bool) {
	bsd.mu.Lock()
	if bsd.alertCount >= bsd.MaxAlerts {
		bsd.mu.Unlock()
		return "", false
	}
	for taskType, count := range bsd.patterns {
		if count >= bsd.Threshold && !bsd.alerted[taskType] {
			bsd.alerted[taskType] = true
			bsd.alertCount++
			msg := fmt.Sprintf("You've asked me to %s %d times. Maybe there's a deeper issue worth addressing?", taskType, count)
			sink := bsd.sink
			sid := bsd.sessionID
			bsd.mu.Unlock()
			// Sink fire OUTSIDE the lock — sink may be slow (disk
			// write), and we don't want to serialise the next
			// Observe behind it.
			if sink != nil {
				func() {
					defer func() { _ = recover() }()
					_ = sink.Create("pattern_detected", msg, sid)
				}()
			}
			return msg, true
		}
	}
	bsd.mu.Unlock()
	return "", false
}

func (bsd *BlindSpotDetector) LoadFromJournal(entries []BlindSpotEntry) {
	bsd.mu.Lock()
	defer bsd.mu.Unlock()
	for _, entry := range entries {
		if entry.Type != "tool_call" && entry.Type != "user_input" {
			continue
		}
		if verb := ExtractVerb(entry.Content); verb != "" {
			bsd.patterns[verb]++
		}
	}
}

// blindSpotVerbs is the canonical verb list. Kept package-level so
// ExtractVerb and LoadFromJournal share the same vocabulary —
// otherwise a verb that gets observed via Observe but not counted
// by LoadFromJournal would silently undercount across boots.
var blindSpotVerbs = []string{
	"fix", "refactor", "debug", "add", "remove", "update",
	"create", "delete", "move", "rename",
}

// ExtractVerb returns the first matching verb in `content`
// (case-insensitive substring), or "" when no canonical verb is
// present. Suitable for live calls from a user-input observer.
func ExtractVerb(content string) string {
	lowered := strings.ToLower(content)
	for _, v := range blindSpotVerbs {
		if strings.Contains(lowered, v) {
			return v
		}
	}
	return ""
}

func (bsd *BlindSpotDetector) Reset() {
	bsd.mu.Lock()
	defer bsd.mu.Unlock()
	bsd.alerted = make(map[string]bool)
	bsd.alertCount = 0
}

// NextWarning returns a single one-line gentle heads-up when a pattern is
// undeniable (count >= Threshold), or "" otherwise. Rate-limited via the
// same MaxAlerts budget Check() uses — once consumed for a pattern, the
// same call will not re-surface it (§4.16).
func (bsd *BlindSpotDetector) NextWarning() string {
	if bsd == nil {
		return ""
	}
	msg, _ := bsd.Check()
	return msg
}

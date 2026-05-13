package personality

import (
	"fmt"
	"strings"
)

type BlindSpotEntry struct {
	Type    string
	Content string
}

type BlindSpotDetector struct {
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
	bsd.patterns[taskType]++
}

func (bsd *BlindSpotDetector) Check() (string, bool) {
	if bsd.alertCount >= bsd.MaxAlerts {
		return "", false
	}
	for taskType, count := range bsd.patterns {
		if count >= bsd.Threshold && !bsd.alerted[taskType] {
			bsd.alerted[taskType] = true
			bsd.alertCount++
			msg := fmt.Sprintf("You've asked me to %s %d times. Maybe there's a deeper issue worth addressing?", taskType, count)
			if bsd.sink != nil {
				func() {
					defer func() { _ = recover() }()
					_ = bsd.sink.Create("pattern_detected", msg, bsd.sessionID)
				}()
			}
			return msg, true
		}
	}
	return "", false
}

func (bsd *BlindSpotDetector) LoadFromJournal(entries []BlindSpotEntry) {
	verbs := []string{"fix", "refactor", "debug", "add", "remove", "update", "create", "delete", "move", "rename"}
	for _, entry := range entries {
		if entry.Type != "tool_call" && entry.Type != "user_input" {
			continue
		}
		lowered := strings.ToLower(entry.Content)
		for _, verb := range verbs {
			if strings.Contains(lowered, verb) {
				bsd.patterns[verb]++
				break
			}
		}
	}
}

func (bsd *BlindSpotDetector) Reset() {
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

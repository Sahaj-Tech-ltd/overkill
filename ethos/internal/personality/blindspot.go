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

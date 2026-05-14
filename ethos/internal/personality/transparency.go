package personality

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

type FailureRecord struct {
	TaskType   string    `json:"task_type"`
	Model      string    `json:"model"`
	Count      int       `json:"count"`
	LastFailed time.Time `json:"last_failed"`
	Pattern    string    `json:"pattern"`
}

type JournalEntry struct {
	Type    string
	Content string
}

// AlertSink is implemented by anything that accepts boot-time alerts (the
// journal AlertStore in production). The package keeps no direct journal
// dependency to avoid cycles.
type AlertSink interface {
	Create(alertType, message, sessionID string) error
}

// TransparencyEngine accumulates failure counts per (taskType, model)
// and surfaces a rate-limited heads-up before the agent retries a
// known-bad path. All mutators + reads take the mutex; concurrent
// access from the journal adapter (RecordFailure on recovery events)
// and the personality provider (per-turn NextWarning) is now race-
// safe.
type TransparencyEngine struct {
	mu           sync.Mutex
	failures     []FailureRecord
	maxWarnings  int
	warned       int
	currentModel string
	sink         AlertSink
	sessionID    string
}

func NewTransparencyEngine(model string) *TransparencyEngine {
	return &TransparencyEngine{
		failures:     nil,
		maxWarnings:  1,
		warned:       0,
		currentModel: model,
	}
}

func (te *TransparencyEngine) RecordFailure(taskType, model string) {
	te.mu.Lock()
	defer te.mu.Unlock()
	for i := range te.failures {
		if te.failures[i].TaskType == taskType && te.failures[i].Model == model {
			te.failures[i].Count++
			te.failures[i].LastFailed = time.Now()
			return
		}
	}
	te.failures = append(te.failures, FailureRecord{
		TaskType:   taskType,
		Model:      model,
		Count:      1,
		LastFailed: time.Now(),
	})
}

// SetAlertSink wires a sink that receives a frustration_signal alert each time
// Check() returns warn-worthy state. Pass nil to disable.
func (te *TransparencyEngine) SetAlertSink(s AlertSink, sessionID string) {
	te.mu.Lock()
	defer te.mu.Unlock()
	te.sink = s
	te.sessionID = sessionID
}

func (te *TransparencyEngine) Check(taskType string) (string, bool) {
	te.mu.Lock()
	for _, f := range te.failures {
		if f.TaskType == taskType && f.Model == te.currentModel {
			if f.Count >= 2 && te.warned < te.maxWarnings {
				te.warned++
				sink := te.sink
				sid := te.sessionID
				count := f.Count
				te.mu.Unlock()
				msg := fmt.Sprintf(
					"Heads up — this type of task (%s) has failed %d times before with this model. Want me to try a different approach?",
					taskType, count,
				)
				if sink != nil {
					func() {
						defer func() { _ = recover() }()
						_ = sink.Create("frustration_signal", msg, sid)
					}()
				}
				return msg, true
			}
			te.mu.Unlock()
			return "", false
		}
	}
	te.mu.Unlock()
	return "", false
}

func (te *TransparencyEngine) LoadFromJournal(entries []JournalEntry) error {
	failurePatterns := []string{
		"tool execution failed",
		"provider error",
		"context cancelled",
	}
	for _, entry := range entries {
		if entry.Type != "error" {
			continue
		}
		content := strings.ToLower(entry.Content)
		matched := false
		for _, p := range failurePatterns {
			if strings.Contains(content, p) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		taskType := extractTaskType(content)
		if taskType == "" {
			continue
		}
		te.RecordFailure(taskType, te.currentModel)
	}
	return nil
}

func (te *TransparencyEngine) ResetWarnings() {
	te.mu.Lock()
	defer te.mu.Unlock()
	te.warned = 0
}

// NextWarning returns a single rate-limited heads-up string for the highest
// failure-count task type, or "" when nothing should be surfaced (no
// qualifying failures, or the per-session warning budget is exhausted).
// It consumes one warning slot, so two calls in a row will not return the
// same warning twice (§4.16 — proactive transparency is rate-limited).
func (te *TransparencyEngine) NextWarning() string {
	if te == nil {
		return ""
	}
	te.mu.Lock()
	if te.warned >= te.maxWarnings {
		te.mu.Unlock()
		return ""
	}
	bestIdx := -1
	bestCount := 0
	for i, f := range te.failures {
		if f.Model != te.currentModel {
			continue
		}
		if f.Count >= 2 && f.Count > bestCount {
			bestCount = f.Count
			bestIdx = i
		}
	}
	if bestIdx < 0 {
		te.mu.Unlock()
		return ""
	}
	te.warned++
	f := te.failures[bestIdx]
	sink := te.sink
	sid := te.sessionID
	te.mu.Unlock()
	msg := fmt.Sprintf(
		"Heads up — this type of task (%s) has failed %d times before with this model. Want me to try a different approach?",
		f.TaskType, f.Count,
	)
	if sink != nil {
		func() {
			defer func() { _ = recover() }()
			_ = sink.Create("frustration_signal", msg, sid)
		}()
	}
	return msg
}

func extractTaskType(content string) string {
	return ExtractTaskType(content)
}

// ExtractTaskType is the exported, case-insensitive task-type
// classifier. The wiring layer (cmd/overkill) uses it to feed
// RecordFailure when the agent's recovery event fires — the last
// user input is what we classify, not the error message.
func ExtractTaskType(content string) string {
	lowered := strings.ToLower(content)
	keywords := []string{
		"refactoring", "debugging", "testing", "deployment",
		"migration", "generation", "compilation", "build",
		"editing", "writing", "analysis", "review",
		// Verbs covered too — overlap with blind-spot vocabulary
		// is intentional: a "refactor" user input feeds both.
		"refactor", "debug", "test", "deploy", "migrate", "build",
		"write", "review",
	}
	for _, kw := range keywords {
		if strings.Contains(lowered, kw) {
			return kw
		}
	}
	return ""
}

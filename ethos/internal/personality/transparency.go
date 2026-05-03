package personality

import (
	"fmt"
	"strings"
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

type TransparencyEngine struct {
	failures     []FailureRecord
	maxWarnings  int
	warned       int
	currentModel string
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

func (te *TransparencyEngine) Check(taskType string) (string, bool) {
	for _, f := range te.failures {
		if f.TaskType == taskType && f.Model == te.currentModel {
			if f.Count >= 2 && te.warned < te.maxWarnings {
				te.warned++
				msg := fmt.Sprintf(
					"Heads up — this type of task (%s) has failed %d times before with this model. Want me to try a different approach?",
					taskType, f.Count,
				)
				return msg, true
			}
			return "", false
		}
	}
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
	te.warned = 0
}

func extractTaskType(content string) string {
	keywords := []string{
		"refactoring", "debugging", "testing", "deployment",
		"migration", "generation", "compilation", "build",
		"editing", "writing", "analysis", "review",
	}
	for _, kw := range keywords {
		if strings.Contains(content, kw) {
			return kw
		}
	}
	return ""
}

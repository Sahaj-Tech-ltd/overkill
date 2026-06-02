package agent

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

type mockJournalWriter struct {
	entries []struct {
		entryType string
		content   string
	}
}

func (m *mockJournalWriter) WriteEntry(entryType string, content string) error {
	m.entries = append(m.entries, struct {
		entryType string
		content   string
	}{entryType, content})
	return nil
}

func TestAnalyze_ProducesReport(t *testing.T) {
	er := NewErrorRecovery(nil)
	report := er.Analyze(errors.New("something broke"), nil)

	if report == nil {
		t.Fatal("expected non-nil report")
	}
	if report.WhatWentWrong != "something broke" {
		t.Errorf("expected WhatWentWrong 'something broke', got %q", report.WhatWentWrong)
	}
}

func TestAnalyze_ExtractsRootCause_NotFound(t *testing.T) {
	er := NewErrorRecovery(nil)
	report := er.Analyze(errors.New("file not found: config.toml"), nil)

	if report.RootCause != "Resource or file does not exist" {
		t.Errorf("expected 'Resource or file does not exist', got %q", report.RootCause)
	}
}

func TestAnalyze_ExtractsRootCause_Timeout(t *testing.T) {
	er := NewErrorRecovery(nil)
	report := er.Analyze(errors.New("connection timeout exceeded"), nil)

	if report.RootCause != "Operation timed out" {
		t.Errorf("expected 'Operation timed out', got %q", report.RootCause)
	}
}

func TestAnalyze_BuildsFaultChain(t *testing.T) {
	history := []providers.Message{
		{Role: "assistant", ToolCalls: []providers.ToolCall{
			{ID: "1", Name: "shell", Arguments: `{"command":"rm -rf /tmp/test"}`},
		}},
		{Role: "tool", Content: "error: /tmp/test: permission denied", ToolCallID: "1"},
	}

	er := NewErrorRecovery(nil)
	report := er.Analyze(errors.New("tool failed: permission denied"), history)

	if len(report.FaultChain) == 0 {
		t.Fatal("expected non-empty fault chain")
	}

	hasAttempt := false
	for _, entry := range report.FaultChain {
		if strings.Contains(entry, "attempted shell") {
			hasAttempt = true
		}
	}
	if !hasAttempt {
		t.Error("expected fault chain to contain attempted tool call")
	}

	hasResult := false
	for _, entry := range report.FaultChain {
		if strings.Contains(entry, "permission denied") {
			hasResult = true
		}
	}
	if !hasResult {
		t.Error("expected fault chain to contain error result")
	}
}

func TestAnalyze_EmptyHistory(t *testing.T) {
	er := NewErrorRecovery(nil)
	report := er.Analyze(errors.New("something failed"), []providers.Message{})

	if report == nil {
		t.Fatal("expected non-nil report for empty history")
	}
	if len(report.FaultChain) != 1 || report.FaultChain[0] != "No prior tool calls to trace" {
		t.Errorf("expected fallback fault chain, got %v", report.FaultChain)
	}
}

func TestFormatRecovery_IncludesApology(t *testing.T) {
	er := NewErrorRecovery(nil)
	report := er.Analyze(errors.New("file not found"), nil)
	formatted := er.FormatRecovery(report)

	if !strings.Contains(formatted, report.Apology) {
		t.Error("expected formatted output to contain apology")
	}
}

func TestFormatRecovery_IncludesFaultChain(t *testing.T) {
	er := NewErrorRecovery(nil)
	report := er.Analyze(errors.New("file not found"), nil)
	formatted := er.FormatRecovery(report)

	for _, entry := range report.FaultChain {
		if !strings.Contains(formatted, entry) {
			t.Errorf("expected formatted output to contain fault chain entry %q", entry)
		}
	}
}

func TestRecordLesson_WritesToJournal(t *testing.T) {
	mock := &mockJournalWriter{}
	er := NewErrorRecovery(mock)
	report := er.Analyze(errors.New("file not found: data.json"), nil)

	err := er.RecordLesson(report)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mock.entries) != 1 {
		t.Fatalf("expected 1 journal entry, got %d", len(mock.entries))
	}
	if mock.entries[0].entryType != "error_lesson" {
		t.Errorf("expected entryType 'error_lesson', got %q", mock.entries[0].entryType)
	}

	var payload map[string]string
	if err := json.Unmarshal([]byte(mock.entries[0].content), &payload); err != nil {
		t.Fatalf("failed to unmarshal journal content: %v", err)
	}
	if payload["WhatWentWrong"] != report.WhatWentWrong {
		t.Errorf("expected WhatWentWrong %q, got %q", report.WhatWentWrong, payload["WhatWentWrong"])
	}
	if payload["RootCause"] != report.RootCause {
		t.Errorf("expected RootCause %q, got %q", report.RootCause, payload["RootCause"])
	}
	if payload["LearningPlan"] != report.LearningPlan {
		t.Errorf("expected LearningPlan %q, got %q", report.LearningPlan, payload["LearningPlan"])
	}
}

func TestRecordLesson_NilWriter_NoPanic(t *testing.T) {
	er := NewErrorRecovery(nil)
	report := er.Analyze(errors.New("something broke"), nil)

	err := er.RecordLesson(report)
	if err != nil {
		t.Fatalf("expected nil error with nil writer, got %v", err)
	}
}

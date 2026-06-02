package personality

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCheck_ReturnsEmptyWhenNoHistory(t *testing.T) {
	te := NewTransparencyEngine("gpt-4")
	msg, ok := te.Check("refactoring")
	assert.False(t, ok)
	assert.Empty(t, msg)
}

func TestCheck_ReturnsWarningAfterFailures(t *testing.T) {
	te := NewTransparencyEngine("gpt-4")
	te.RecordFailure("refactoring", "gpt-4")
	te.RecordFailure("refactoring", "gpt-4")
	msg, ok := te.Check("refactoring")
	assert.True(t, ok)
	assert.Contains(t, msg, "refactoring")
	assert.Contains(t, msg, "failed 2 times")
}

func TestCheck_ReturnsEmptyAfterMaxWarnings(t *testing.T) {
	te := NewTransparencyEngine("gpt-4")
	te.RecordFailure("refactoring", "gpt-4")
	te.RecordFailure("refactoring", "gpt-4")
	_, first := te.Check("refactoring")
	assert.True(t, first)
	_, second := te.Check("refactoring")
	assert.False(t, second)
}

func TestCheck_ModelVersioned(t *testing.T) {
	te := NewTransparencyEngine("gpt-4")
	te.RecordFailure("refactoring", "gpt-3.5")
	te.RecordFailure("refactoring", "gpt-3.5")
	msg, ok := te.Check("refactoring")
	assert.False(t, ok)
	assert.Empty(t, msg)
}

func TestRecordFailure_Increments(t *testing.T) {
	te := NewTransparencyEngine("gpt-4")
	te.RecordFailure("debugging", "gpt-4")
	te.RecordFailure("debugging", "gpt-4")
	te.RecordFailure("debugging", "gpt-4")
	assert.Len(t, te.failures, 1)
	assert.Equal(t, 3, te.failures[0].Count)
	msg, ok := te.Check("debugging")
	assert.True(t, ok)
	assert.Contains(t, msg, "failed 3 times")
}

func TestLoadFromJournal_PopulatesHistory(t *testing.T) {
	te := NewTransparencyEngine("claude-3")
	entries := []JournalEntry{
		{Type: "error", Content: "tool execution failed during refactoring"},
		{Type: "error", Content: "provider error while running refactoring"},
		{Type: "info", Content: "task completed successfully"},
	}
	err := te.LoadFromJournal(entries)
	assert.NoError(t, err)
	msg, ok := te.Check("refactoring")
	assert.True(t, ok)
	assert.Contains(t, msg, "refactoring")
}

func TestResetWarnings_AllowsNewWarnings(t *testing.T) {
	te := NewTransparencyEngine("gpt-4")
	te.RecordFailure("testing", "gpt-4")
	te.RecordFailure("testing", "gpt-4")
	_, first := te.Check("testing")
	assert.True(t, first)
	_, second := te.Check("testing")
	assert.False(t, second)
	te.ResetWarnings()
	_, third := te.Check("testing")
	assert.True(t, third)
}

func TestCheck_CountBelowThreshold(t *testing.T) {
	te := NewTransparencyEngine("gpt-4")
	te.RecordFailure("deployment", "gpt-4")
	msg, ok := te.Check("deployment")
	assert.False(t, ok)
	assert.Empty(t, msg)
}

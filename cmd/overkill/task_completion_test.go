package main

import (
	"strings"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/automation"
)

func TestFormatTaskCompletionMessage_AllStates(t *testing.T) {
	cases := []struct {
		name     string
		task     automation.LedgerTask
		contains []string
	}{
		{
			name:     "completed with result",
			task:     automation.LedgerTask{State: automation.TaskCompleted, Source: "alarm", Name: "build", Result: "ok"},
			contains: []string{"alarm/build", "completed", "ok"},
		},
		{
			name:     "completed no result",
			task:     automation.LedgerTask{State: automation.TaskCompleted, Source: "cron", Name: "backup"},
			contains: []string{"cron/backup", "completed"},
		},
		{
			name:     "failed with error",
			task:     automation.LedgerTask{State: automation.TaskFailed, Source: "alarm", Error: "disk full"},
			contains: []string{"failed", "disk full"},
		},
		{
			name:     "cancelled",
			task:     automation.LedgerTask{State: automation.TaskCancelled, Source: "x"},
			contains: []string{"cancelled"},
		},
		{
			name:     "timed out",
			task:     automation.LedgerTask{State: automation.TaskTimedOut, Source: "x"},
			contains: []string{"timed out"},
		},
		{
			name:     "lost",
			task:     automation.LedgerTask{State: automation.TaskLost, Source: "x"},
			contains: []string{"lost", "no heartbeat"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := formatTaskCompletionMessage(c.task)
			for _, want := range c.contains {
				if !strings.Contains(strings.ToLower(got), strings.ToLower(want)) {
					t.Errorf("missing %q in %q", want, got)
				}
			}
		})
	}
}

func TestFirstLineSummary_TruncatesAndPicksFirstNonEmpty(t *testing.T) {
	if got := firstLineSummary("first\nsecond\nthird"); got != "first" {
		t.Errorf("expected first line, got %q", got)
	}
	if got := firstLineSummary("\n\nfirst real line"); got != "first real line" {
		t.Errorf("should skip empty leading lines, got %q", got)
	}
	long := strings.Repeat("x", 200)
	got := firstLineSummary(long)
	if !strings.HasSuffix(got, "…") {
		t.Errorf("long line should be truncated: %q", got)
	}
}

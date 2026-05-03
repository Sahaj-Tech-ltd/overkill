package status

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	tuitypes "github.com/Sahaj-Tech-ltd/ethos/pkg/tui/types"
)

func TestStatusBar_Init(t *testing.T) {
	// Init returns nil now — spinner is gated until the agent goes busy
	// to avoid wasted SSH redraws while idle.
	sb := NewStatusBar()
	if cmd := sb.Init(); cmd != nil {
		t.Error("Init should not start ticking while idle")
	}
}

func TestStatusBar_UpdateSpinnerOnlyWhenBusy(t *testing.T) {
	// Idle: tick is a no-op and does not reschedule.
	sb := NewStatusBar()
	sb.spinnerFrame = 0
	updated, cmd := sb.Update(spinnerTickMsg{})
	if updated.spinnerFrame != 0 {
		t.Errorf("idle tick should not advance frame, got %d", updated.spinnerFrame)
	}
	if cmd != nil {
		t.Error("idle tick should not reschedule")
	}

	// Busy: tick advances and reschedules.
	sb.state = tuitypes.StatusGenerating
	updated, cmd = sb.Update(spinnerTickMsg{})
	if updated.spinnerFrame != 1 {
		t.Errorf("busy tick should advance to 1, got %d", updated.spinnerFrame)
	}
	if cmd == nil {
		t.Error("busy tick should reschedule")
	}
}

func TestStatusBar_StateChangeKicksSpinner(t *testing.T) {
	sb := NewStatusBar()
	if _, cmd := sb.Update(StateChangeMsg{State: tuitypes.StatusGenerating}); cmd == nil {
		t.Error("idle→busy edge should start the spinner tick")
	}
}

func TestStatusBar_ViewIdleHasModelLabel(t *testing.T) {
	sb := NewStatusBar()
	sb.width = 80
	sb.SetModel("gpt-4o", "openai")
	v := sb.View()
	if !strings.Contains(v, "gpt-4o") {
		t.Errorf("expected model name, got %q", v)
	}
	if !strings.Contains(v, "openai") {
		t.Errorf("expected provider, got %q", v)
	}
}

func TestStatusBar_ViewToolCallShowsToolName(t *testing.T) {
	sb := NewStatusBar()
	sb.width = 80
	sb.state = tuitypes.StatusToolCall
	sb.toolName = "shell"
	v := sb.View()
	if !strings.Contains(v, "shell") {
		t.Error("should show tool name when tool call active")
	}
}

func TestStatusBar_TokensAndCost(t *testing.T) {
	sb := NewStatusBar()
	sb.width = 100
	sb.inputTokens = 1200
	sb.totalCost = 0.05
	v := sb.View()
	if !strings.Contains(v, "1.2k") {
		t.Errorf("expected token count, got %q", v)
	}
	if !strings.Contains(v, "$0.05") {
		t.Errorf("expected cost, got %q", v)
	}
}

func TestStatusBar_Resize(t *testing.T) {
	sb := NewStatusBar()
	updated, _ := sb.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	if updated.width != 120 {
		t.Error("width not updated")
	}
}

func TestStatusBar_CompactCwdReplacesHome(t *testing.T) {
	t.Setenv("HOME", "/home/test")
	got := compactCwd("/home/test/projects/foo")
	if got != "~/projects/foo" {
		t.Errorf("expected ~/projects/foo, got %q", got)
	}
}

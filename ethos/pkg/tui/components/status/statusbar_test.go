package status

import (
	"testing"
	tea "github.com/charmbracelet/bubbletea"
	tuitypes "github.com/Sahaj-Tech-ltd/ethos/pkg/tui/types"
)

func TestStatusBar_Init(t *testing.T) {
	sb := NewStatusBar()
	cmd := sb.Init()
	if cmd == nil { t.Error("Init should return cmd") }
}

func TestStatusBar_UpdateSpinner(t *testing.T) {
	sb := NewStatusBar()
	sb.spinnerFrame = 0
	updated, _ := sb.Update(spinnerTickMsg{})
	if updated.spinnerFrame != 1 { t.Errorf("expected 1, got %d", updated.spinnerFrame) }
}

func TestStatusBar_ViewIdle(t *testing.T) {
	sb := NewStatusBar()
	sb.state = tuitypes.StatusIdle
	v := sb.View()
	if !contains(v, "Ready") { t.Error("idle should show Ready") }
}

func TestStatusBar_ViewThinking(t *testing.T) {
	sb := NewStatusBar()
	sb.state = tuitypes.StatusThinking
	v := sb.View()
	if !contains(v, "Thinking") { t.Error("should show Thinking") }
}

func TestStatusBar_ViewGenerating(t *testing.T) {
	sb := NewStatusBar()
	sb.state = tuitypes.StatusGenerating
	v := sb.View()
	if !contains(v, "Generating") { t.Error("should show Generating") }
}

func TestStatusBar_ViewToolCall(t *testing.T) {
	sb := NewStatusBar()
	sb.state = tuitypes.StatusToolCall
	sb.toolName = "shell"
	v := sb.View()
	if !contains(v, "shell") { t.Error("should show tool name") }
}

func TestStatusBar_CostDisplay(t *testing.T) {
	sb := NewStatusBar()
	sb.inputTokens = 1200
	sb.totalCost = 0.05
	v := sb.View()
	if !contains(v, "1.2K") || !contains(v, "$0.05") { t.Error("should show tokens and cost") }
}

func TestStatusBar_CostWarning(t *testing.T) {
	sb := NewStatusBar()
	sb.contextPct = 0.85
	v := sb.View()
	if !contains(v, "85%") { t.Error("should show context percent") }
}

func TestStatusBar_Personality(t *testing.T) {
	sb := NewStatusBar()
	sb.personalityMode = "Witty"
	v := sb.View()
	if !contains(v, "Witty") { t.Error("should show personality mode") }
}

func TestStatusBar_SessionName(t *testing.T) {
	sb := NewStatusBar()
	sb.sessionName = "debug-session"
	v := sb.View()
	if !contains(v, "debug-session") { t.Error("should show session name") }
}

func TestStatusBar_ContextPercent(t *testing.T) {
	sb := NewStatusBar()
	sb.contextPct = 0.45
	v := sb.View()
	if !contains(v, "45%") { t.Error("should show 45%") }
}

func TestStatusBar_Resize(t *testing.T) {
	sb := NewStatusBar()
	updated, _ := sb.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	if updated.width != 120 { t.Error("width not updated") }
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsStr(s, sub))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

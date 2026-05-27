package sidebar

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func containsStr(s, substr string) bool {
	return strings.Contains(s, substr)
}

func TestSidebar_Init(t *testing.T) {
	sb := NewSidebar()
	cmd := sb.Init()
	if cmd != nil {
		t.Error("Init should return nil for container")
	}
}

func TestSidebar_SwitchTab(t *testing.T) {
	sb := NewSidebar()
	sb.SetSize(30, 20)
	updated, _ := sb.Update(tea.KeyMsg{Type: tea.KeyTab})
	if updated.activeTab != 1 {
		t.Errorf("expected tab 1, got %d", updated.activeTab)
	}
}

func TestSidebar_ActiveHighlight(t *testing.T) {
	sb := NewSidebar()
	sb.SetSize(30, 20)
	v := sb.View()
	if !containsStr(v, "Cost") {
		t.Error("active tab should be visible")
	}
}

func TestSidebar_Render(t *testing.T) {
	sb := NewSidebar()
	sb.SetSize(30, 20)
	v := sb.View()
	if v == "" {
		t.Error("should render something")
	}
}

func TestSidebar_Resize(t *testing.T) {
	sb := NewSidebar()
	sb.SetSize(30, 20)
	w, h := sb.GetSize()
	if w != 30 || h != 20 {
		t.Error("size mismatch")
	}
}

func TestSidebar_Focus(t *testing.T) {
	sb := NewSidebar()
	sb.Focus()
	if !sb.IsFocused() {
		t.Error("should be focused")
	}
	sb.Blur()
	if sb.IsFocused() {
		t.Error("should not be focused")
	}
}

func TestSidebar_NoContent(t *testing.T) {
	sb := NewSidebar()
	sb.SetSize(30, 20)
	v := sb.View()
	if !containsStr(v, "Cost") && !containsStr(v, "Files") {
		t.Error("should show tabs")
	}
}

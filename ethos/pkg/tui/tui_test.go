package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func containsStr(s, substr string) bool {
	return strings.Contains(s, substr)
}

func TestSplitPane_Ratio(t *testing.T) {
	model := New(nil)
	m := model.(*appModel)
	m.width = 100
	m.height = 40
	m.showSidebar = true
	m.sidebar.SetSize(defaultSidebarWidth, m.height)
	v := m.View()
	if v == "" {
		t.Error("view should not be empty")
	}
}

func TestSplitPane_MinWidth(t *testing.T) {
	model := New(nil)
	m := model.(*appModel)
	m.width = 50
	m.height = 30
	m.showSidebar = true
	m.sidebar.SetSize(defaultSidebarWidth, m.height)
	v := m.View()
	if v == "" {
		t.Error("view should not be empty at min width")
	}
}

func TestSplitPane_Resize(t *testing.T) {
	model := New(nil)
	m := model.(*appModel)
	_, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	if m.width != 100 {
		t.Errorf("expected width 100, got %d", m.width)
	}
	_, _ = m.Update(tea.WindowSizeMsg{Width: 60, Height: 30})
	if m.width != 60 {
		t.Errorf("expected width 60, got %d", m.width)
	}
}

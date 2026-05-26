package dialog

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestDialog_Base(t *testing.T) {
	d := Dialog{Width: 40, Height: 10}
	d.ShowDialog()
	v := d.BaseView("hello", 80, 24)
	if !strings.Contains(v, "hello") {
		t.Error("should contain content")
	}
	if !strings.Contains(v, "╭") && !strings.Contains(v, "─") {
		t.Error("should have border")
	}
}

func TestDialog_CloseOnEsc(t *testing.T) {
	if !CloseOnEsc(tea.KeyMsg{Type: tea.KeyEsc}) {
		t.Error("esc should close")
	}
	if CloseOnEsc(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}}) {
		t.Error("non-esc should not close")
	}
}

func TestDialog_CloseMsg(t *testing.T) {
	d := Dialog{Show: true}
	d.HideDialog()
	if d.Show {
		t.Error("should be hidden")
	}
}

func TestDialog_Resize(t *testing.T) {
	d := Dialog{}
	d.SetSize(80, 24)
	if d.Width != 80 || d.Height != 24 {
		t.Error("size not set")
	}
}

func TestDialog_BlockKeys(t *testing.T) {
	if !BlockKeys(tea.KeyMsg{Type: tea.KeyRunes}, true) {
		t.Error("should block when shown")
	}
	if BlockKeys(tea.KeyMsg{Type: tea.KeyRunes}, false) {
		t.Error("should not block when hidden")
	}
	if BlockKeys(tea.WindowSizeMsg{}, true) {
		t.Error("should not block non-key")
	}
}

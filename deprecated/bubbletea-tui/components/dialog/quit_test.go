package dialog

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestQuit_Show(t *testing.T) {
	q := NewQuitDialog()
	q.Update(ShowQuitMsg{})
	if !q.Show {
		t.Error("should be shown")
	}
}

func TestQuit_Confirm(t *testing.T) {
	q := NewQuitDialog()
	q.Show = true
	_, cmd := q.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Error("y should return quit cmd")
	}
}

func TestQuit_Cancel(t *testing.T) {
	q := NewQuitDialog()
	q.Show = true
	updated, _ := q.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	if updated.Show {
		t.Error("should be hidden after n")
	}
}

func TestQuit_View(t *testing.T) {
	q := NewQuitDialog()
	q.Show = true
	v := q.View(80, 24)
	if !containsStr(v, "Quit") || !containsStr(v, "y") || !containsStr(v, "n") {
		t.Error("should show quit prompt")
	}
}

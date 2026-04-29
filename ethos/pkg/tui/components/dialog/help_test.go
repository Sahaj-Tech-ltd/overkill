package dialog

import (
	"testing"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
)

func TestHelp_Show(t *testing.T) {
	h := NewHelpDialog()
	h.Update(ShowHelpMsg{})
	if !h.Show {
		t.Error("should be shown")
	}
}

func TestHelp_Bindings(t *testing.T) {
	h := NewHelpDialog()
	bindings := []key.Binding{
		key.NewBinding(key.WithKeys("ctrl+c"), key.WithHelp("ctrl+c", "quit")),
		key.NewBinding(key.WithKeys("ctrl+s"), key.WithHelp("ctrl+s", "sessions")),
		key.NewBinding(key.WithKeys("ctrl+k"), key.WithHelp("ctrl+k", "commands")),
	}
	h.SetBindings(bindings)
	h.Show = true
	v := h.View(80, 24)
	if !containsStr(v, "quit") || !containsStr(v, "sessions") || !containsStr(v, "commands") {
		t.Error("should show all bindings")
	}
}

func TestHelp_Close(t *testing.T) {
	h := NewHelpDialog()
	h.Show = true
	updated, _ := h.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if updated.Show {
		t.Error("should be hidden after esc")
	}
}

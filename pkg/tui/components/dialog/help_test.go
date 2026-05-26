package dialog

import (
	"strings"
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
	v := h.View(100, 40)
	if !containsStr(v, "quit") || !containsStr(v, "sessions") || !containsStr(v, "commands") {
		t.Error("should show all bindings")
	}
	if !containsStr(v, "Keybindings") {
		t.Error("should render Keybindings section header")
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

func TestHelp_FilterCrossSection(t *testing.T) {
	h := NewHelpDialog()
	h.SetCommands([]Command{
		{ID: "diff", Title: "/diff", Description: "show a unified diff for a path"},
		{ID: "model", Title: "/model", Description: "open model picker"},
	})
	h.SetBindings([]key.Binding{
		key.NewBinding(key.WithKeys("ctrl+o"), key.WithHelp("ctrl+o", "models")),
	})
	h.Show = true
	for _, r := range "diff" {
		h.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	(&h).flatten()
	found := false
	for _, e := range h.entries {
		if strings.Contains(e.Label, "/diff") {
			found = true
		}
		if strings.Contains(e.Label, "/model") {
			t.Errorf("/model should be filtered out: %+v", e)
		}
	}
	if !found {
		t.Error("expected /diff to survive filter")
	}
}

func TestHelp_EnterEmitsSelection(t *testing.T) {
	h := NewHelpDialog()
	h.SetCommands([]Command{
		{ID: "help", Title: "/help", Description: "show keybinding help"},
	})
	h.Show = true
	(&h).flatten()
	_, cmd := h.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter on a command entry should return a cmd")
	}
	msg := cmd()
	sel, ok := msg.(HelpEntrySelectedMsg)
	if !ok {
		t.Fatalf("expected HelpEntrySelectedMsg, got %T", msg)
	}
	if sel.Entry.Action != "help" {
		t.Errorf("wrong action: %q", sel.Entry.Action)
	}
}

func TestHelp_SectionsInOrder(t *testing.T) {
	h := NewHelpDialog()
	h.SetCommands([]Command{{ID: "help", Title: "/help", Description: "h"}})
	h.SetBindings([]key.Binding{
		key.NewBinding(key.WithKeys("ctrl+h"), key.WithHelp("ctrl+h", "help")),
	})
	h.SetDialogs([]HelpEntry{{Label: "models", Detail: "model picker"}})
	h.SetAbout(HelpAbout{Version: "0.1.0"})
	h.Show = true
	v := h.View(120, 40)
	cmdIdx := strings.Index(v, "Commands")
	kbIdx := strings.Index(v, "Keybindings")
	dlgIdx := strings.Index(v, "Dialogs")
	aboutIdx := strings.Index(v, "About")
	if cmdIdx < 0 || kbIdx < 0 || dlgIdx < 0 || aboutIdx < 0 {
		t.Fatalf("missing section headers in:\n%s", v)
	}
	if !(cmdIdx < kbIdx && kbIdx < dlgIdx && dlgIdx < aboutIdx) {
		t.Errorf("sections out of order: cmd=%d kb=%d dlg=%d about=%d",
			cmdIdx, kbIdx, dlgIdx, aboutIdx)
	}
}

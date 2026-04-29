package dialog

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func containsStr(s, sub string) bool {
	return strings.Contains(s, sub)
}

func TestCommandPalette_Show(t *testing.T) {
	cd := NewCommandDialog()
	cd.Update(ShowCommandDialogMsg{})
	if !cd.Show {
		t.Error("should be shown")
	}
}

func TestCommandPalette_List(t *testing.T) {
	cd := NewCommandDialog()
	cd.RegisterCommand(Command{ID: "a", Title: "Compact Session", Description: "Compact"})
	cd.RegisterCommand(Command{ID: "b", Title: "Init Project", Description: "Init"})
	cd.RegisterCommand(Command{ID: "c", Title: "Run Doctor", Description: "Doctor"})
	cd.Show = true
	v := cd.View(80, 24)
	if !containsStr(v, "Compact") || !containsStr(v, "Init") || !containsStr(v, "Doctor") {
		t.Error("should show all commands")
	}
}

func TestCommandPalette_FuzzyMatch(t *testing.T) {
	cd := NewCommandDialog()
	cd.RegisterCommand(Command{ID: "a", Title: "Compact Session", Description: "Compact"})
	cd.RegisterCommand(Command{ID: "b", Title: "Init Project", Description: "Init"})
	cd.Show = true
	updated, _ := cd.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c', 'o', 'm'}})
	v := updated.View(80, 24)
	if !containsStr(v, "Compact") {
		t.Error("should match compact")
	}
	if containsStr(v, "Init") {
		t.Error("should not show init")
	}
}

func TestCommandPalette_Select(t *testing.T) {
	cd := NewCommandDialog()
	cd.RegisterCommand(Command{ID: "a", Title: "Test"})
	cd.Show = true
	_, cmd := cd.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{13}})
	if cmd == nil {
		t.Error("enter should return cmd")
	}
}

func TestCommandPalette_Highlight(t *testing.T) {
	cd := NewCommandDialog()
	cd.RegisterCommand(Command{ID: "a", Title: "First"})
	cd.RegisterCommand(Command{ID: "b", Title: "Second"})
	cd.Show = true
	updated, _ := cd.Update(tea.KeyMsg{Type: tea.KeyDown})
	v := updated.View(80, 24)
	if !containsStr(v, ">") {
		t.Error("should have cursor")
	}
}

func TestCommandPalette_Empty(t *testing.T) {
	cd := NewCommandDialog()
	cd.Show = true
	updated, _ := cd.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'z', 'z', 'z'}})
	v := updated.View(80, 24)
	if !containsStr(v, "No matching") {
		t.Error("should show empty state")
	}
}

func TestCommandPalette_Register(t *testing.T) {
	cd := NewCommandDialog()
	before := len(cd.Commands)
	cd.RegisterCommand(Command{ID: "new", Title: "New"})
	if len(cd.Commands) != before+1 {
		t.Error("should add command")
	}
}

func TestCommandPalette_Handler(t *testing.T) {
	cd := NewCommandDialog()
	cd.RegisterCommand(Command{
		ID:    "test",
		Title: "Test",
		Handler: func(c Command) tea.Cmd {
			return nil
		},
	})
	cd.Show = true
	cd.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{13}})
}

func TestCommandPalette_Close(t *testing.T) {
	cd := NewCommandDialog()
	cd.Show = true
	updated, _ := cd.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if updated.Show {
		t.Error("should be hidden after esc")
	}
}

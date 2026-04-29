package dialog

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestModelPicker_Show(t *testing.T) {
	m := NewModelDialog()
	updated, _ := m.Update(ShowModelDialogMsg{})
	if !updated.Show {
		t.Error("ShowModelDialogMsg should set Show=true")
	}
}

func TestModelPicker_ListModels(t *testing.T) {
	m := NewModelDialog()
	m.SetModels([]ModelEntry{
		{ID: "gpt-4", Name: "GPT-4", Provider: "openai"},
		{ID: "claude-3", Name: "Claude 3", Provider: "anthropic"},
		{ID: "gemini-pro", Name: "Gemini Pro", Provider: "google"},
		{ID: "llama-3", Name: "Llama 3", Provider: "ollama"},
		{ID: "mixtral", Name: "Mixtral", Provider: "ollama"},
	})
	m.Show = true
	v := m.View(80, 24)
	for _, name := range []string{"GPT-4", "Claude 3", "Gemini Pro", "Llama 3", "Mixtral"} {
		if !strings.Contains(v, name) {
			t.Errorf("view should contain %q", name)
		}
	}
}

func TestModelPicker_FilterByProvider(t *testing.T) {
	m := NewModelDialog()
	m.SetModels([]ModelEntry{
		{ID: "gpt-4", Name: "GPT-4", Provider: "openai"},
		{ID: "claude-3", Name: "Claude 3", Provider: "anthropic"},
		{ID: "llama-3", Name: "Llama 3", Provider: "ollama"},
		{ID: "mixtral", Name: "Mixtral", Provider: "ollama"},
	})
	m.Show = true
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	v := updated.View(80, 24)
	if strings.Contains(v, "GPT-4") {
		t.Error("tab should filter to next provider, should not show GPT-4")
	}
}

func TestModelPicker_SelectModel(t *testing.T) {
	m := NewModelDialog()
	m.SetModels([]ModelEntry{
		{ID: "gpt-4", Name: "GPT-4", Provider: "openai"},
		{ID: "claude-3", Name: "Claude 3", Provider: "anthropic"},
	})
	m.Show = true
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter should return a command")
	}
	msg := cmd()
	sel, ok := msg.(ModelSelectedMsg)
	if !ok {
		t.Fatal("command should return ModelSelectedMsg")
	}
	if sel.ModelID != "gpt-4" {
		t.Errorf("expected model ID gpt-4, got %q", sel.ModelID)
	}
}

func TestModelPicker_FuzzySearch(t *testing.T) {
	m := NewModelDialog()
	m.SetModels([]ModelEntry{
		{ID: "gpt-4", Name: "GPT-4", Provider: "openai"},
		{ID: "gpt-3.5", Name: "GPT-3.5 Turbo", Provider: "openai"},
		{ID: "claude-3", Name: "Claude 3", Provider: "anthropic"},
	})
	m.Show = true
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g', 'p', 't'}})
	v := updated.View(80, 24)
	if !strings.Contains(v, "GPT-4") || !strings.Contains(v, "GPT-3.5") {
		t.Error("filtered results should contain GPT models")
	}
	if strings.Contains(v, "Claude") {
		t.Error("filtered results should not contain Claude")
	}
}

func TestModelPicker_Highlight(t *testing.T) {
	m := NewModelDialog()
	m.SetModels([]ModelEntry{
		{ID: "gpt-4", Name: "GPT-4", Provider: "openai"},
		{ID: "claude-3", Name: "Claude 3", Provider: "anthropic"},
	})
	m.Show = true
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	v := updated.View(80, 24)
	lines := strings.Split(v, "\n")
	found := false
	for _, line := range lines {
		if strings.Contains(line, ">") && strings.Contains(line, "Claude") {
			found = true
			break
		}
	}
	if !found {
		t.Error("cursor should highlight second model with >")
	}
}

func TestModelPicker_MaxWidth(t *testing.T) {
	m := NewModelDialog()
	longName := strings.Repeat("x", 50)
	m.SetModels([]ModelEntry{
		{ID: "long", Name: longName, Provider: "test"},
	})
	m.Show = true
	v := m.View(80, 24)
	for _, line := range strings.Split(v, "\n") {
		if len(line) > 200 {
			t.Errorf("line too long: %d chars", len(line))
		}
	}
	if !strings.Contains(v, "...") {
		t.Error("long name should be truncated with ...")
	}
}

func TestModelPicker_Close(t *testing.T) {
	m := NewModelDialog()
	m.Show = true
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("esc should return a command")
	}
	msg := cmd()
	if _, ok := msg.(CloseModelDialogMsg); !ok {
		t.Error("esc should return CloseModelDialogMsg")
	}
}

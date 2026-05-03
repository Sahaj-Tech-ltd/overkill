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

// drillTo selects the provider matching prov by walking the step-0 list
// with Down arrows and pressing Enter. Helper for the two-step picker tests.
func drillTo(t *testing.T, m ModelDialog, prov string) ModelDialog {
	t.Helper()
	for i, p := range m.Providers {
		if p == prov {
			for j := 0; j < i; j++ {
				m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
			}
			m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			return m
		}
	}
	t.Fatalf("provider %q not found in %v", prov, m.Providers)
	return m
}

func TestModelPicker_ListProviders(t *testing.T) {
	// At step 0 the dialog lists provider IDs/names, not models.
	m := NewModelDialog()
	m.SetModels([]ModelEntry{
		{ID: "gpt-4", Name: "GPT-4", Provider: "openai", ProviderName: "OpenAI"},
		{ID: "claude-3", Name: "Claude 3", Provider: "anthropic", ProviderName: "Anthropic"},
		{ID: "gemini-pro", Name: "Gemini Pro", Provider: "google", ProviderName: "Google"},
	})
	m.Show = true
	v := m.View(80, 24)
	for _, label := range []string{"OpenAI", "Anthropic", "Google"} {
		if !strings.Contains(v, label) {
			t.Errorf("step 0 view should contain provider %q", label)
		}
	}
	// Models should NOT be visible until the user drills into one.
	if strings.Contains(v, "GPT-4") {
		t.Error("step 0 view should not list models")
	}
}

func TestModelPicker_DrillIntoProvider(t *testing.T) {
	m := NewModelDialog()
	m.SetModels([]ModelEntry{
		{ID: "gpt-4", Name: "GPT-4", Provider: "openai"},
		{ID: "claude-3", Name: "Claude 3", Provider: "anthropic"},
	})
	m.Show = true
	m = drillTo(t, m, "openai")
	v := m.View(80, 24)
	if !strings.Contains(v, "GPT-4") {
		t.Error("after drilling into openai, GPT-4 should be visible")
	}
	if strings.Contains(v, "Claude 3") {
		t.Error("after drilling into openai, Claude 3 should not be visible")
	}
}

func TestModelPicker_BackspaceReturnsToProviders(t *testing.T) {
	m := NewModelDialog()
	m.SetModels([]ModelEntry{
		{ID: "gpt-4", Name: "GPT-4", Provider: "openai", ProviderName: "OpenAI"},
		{ID: "claude-3", Name: "Claude 3", Provider: "anthropic", ProviderName: "Anthropic"},
	})
	m.Show = true
	m = drillTo(t, m, "openai")
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if m.step != 0 {
		t.Errorf("backspace from step 1 should return to step 0, got step=%d", m.step)
	}
	v := m.View(80, 24)
	if !strings.Contains(v, "OpenAI") {
		t.Error("after returning to step 0, providers should list")
	}
}

func TestModelPicker_SelectModel(t *testing.T) {
	m := NewModelDialog()
	m.SetModels([]ModelEntry{
		{ID: "gpt-4", Name: "GPT-4", Provider: "openai"},
		{ID: "claude-3", Name: "Claude 3", Provider: "anthropic"},
	})
	m.Show = true
	m = drillTo(t, m, "openai")
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter on a model row should return a command")
	}
	sel, ok := cmd().(ModelSelectedMsg)
	if !ok {
		t.Fatal("command should return ModelSelectedMsg")
	}
	if sel.ModelID != "gpt-4" || sel.Provider != "openai" {
		t.Errorf("expected gpt-4/openai, got %q/%q", sel.ModelID, sel.Provider)
	}
}

func TestModelPicker_FuzzySearchModels(t *testing.T) {
	// Filtering at step 1 narrows the model list within the chosen provider.
	m := NewModelDialog()
	m.SetModels([]ModelEntry{
		{ID: "gpt-4", Name: "GPT-4", Provider: "openai"},
		{ID: "gpt-3.5", Name: "GPT-3.5 Turbo", Provider: "openai"},
		{ID: "o1", Name: "o1", Provider: "openai"},
	})
	m.Show = true
	m = drillTo(t, m, "openai")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g', 'p', 't'}})
	v := updated.View(80, 24)
	if !strings.Contains(v, "GPT-4") || !strings.Contains(v, "GPT-3.5") {
		t.Error("filter should keep GPT-* rows")
	}
	if strings.Contains(v, "o1") {
		t.Error("filter should remove o1 row")
	}
}

func TestModelPicker_Highlight(t *testing.T) {
	m := NewModelDialog()
	m.SetModels([]ModelEntry{
		{ID: "gpt-4", Name: "GPT-4", Provider: "openai"},
		{ID: "gpt-3.5", Name: "GPT-3.5", Provider: "openai"},
	})
	m.Show = true
	m = drillTo(t, m, "openai")
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	v := updated.View(80, 24)
	found := false
	for _, line := range strings.Split(v, "\n") {
		if strings.Contains(line, ">") && strings.Contains(line, "GPT-3.5") {
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
	m = drillTo(t, m, "test")
	v := m.View(80, 24)
	for _, line := range strings.Split(v, "\n") {
		if len(line) > 400 {
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

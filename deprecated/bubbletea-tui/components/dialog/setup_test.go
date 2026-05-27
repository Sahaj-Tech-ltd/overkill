package dialog

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

func TestSetupDialog_ProviderNavigation(t *testing.T) {
	d := NewSetupDialog()
	d.Show = true

	updated, _ := d.Update(tea.KeyMsg{Type: tea.KeyDown})
	if updated.providerIdx != 1 {
		t.Errorf("down should move to index 1, got %d", updated.providerIdx)
	}
	if updated.providers[updated.providerIdx] != "anthropic" {
		t.Errorf("expected anthropic, got %s", updated.providers[updated.providerIdx])
	}
}

func TestSetupDialog_ProviderWrapAround(t *testing.T) {
	d := NewSetupDialog()
	d.Show = true

	for i := 0; i < len(d.providers); i++ {
		d, _ = d.Update(tea.KeyMsg{Type: tea.KeyUp})
	}
	if d.providerIdx != 0 {
		t.Errorf("wrapping up should return to last and back to 0, got %d", d.providerIdx)
	}
}

func TestSetupDialog_ProviderSelectAdvancesStep(t *testing.T) {
	d := NewSetupDialog()
	d.Show = true

	updated, _ := d.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if updated.step != 1 {
		t.Errorf("enter on provider should advance to step 1 (api key), got step %d", updated.step)
	}
}

func TestSetupDialog_KeyEditingStart(t *testing.T) {
	d := NewSetupDialog()
	d.Show = true
	d.step = 1

	if d.editingKey {
		t.Error("editingKey should start false")
	}
	updated, _ := d.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !updated.editingKey {
		t.Error("enter should set editingKey to true")
	}
	if updated.apiKey != "" {
		t.Errorf("apiKey should be cleared on edit start, got %q", updated.apiKey)
	}
}

func TestSetupDialog_KeyTyping(t *testing.T) {
	d := NewSetupDialog()
	d.Show = true
	d.step = 1
	d.editingKey = true

	updated, _ := d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'-'}})
	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}})
	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})

	if updated.apiKey != "sk-test" {
		t.Errorf("expected apiKey 'sk-test', got %q", updated.apiKey)
	}

	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if updated.apiKey != "sk-tes" {
		t.Errorf("backspace should remove last char, got %q", updated.apiKey)
	}
}

func TestSetupDialog_KeyConfirmAdvancesToModel(t *testing.T) {
	d := NewSetupDialog()
	d.Show = true
	d.step = 1
	d.providerIdx = 0
	d.editingKey = true
	d.apiKey = "sk-key123"

	updated, cmd := d.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if updated.step != 2 {
		t.Errorf("enter on key confirm should advance to step 2 (model), got step %d", updated.step)
	}
	if updated.editingKey {
		t.Error("editingKey should be false after confirm")
	}
	if cmd == nil {
		t.Log("no command returned on key confirm (models need loading)")
	}
}

func TestSetupDialog_ModelNavigation(t *testing.T) {
	d := NewSetupDialog()
	d.Show = true
	d.step = 2
	d.models = []providers.Model{
		{ID: "gpt-4o", Name: "GPT-4o", CostIn: 2.50, CostOut: 10.00},
		{ID: "gpt-4o-mini", Name: "GPT-4o Mini", CostIn: 0.15, CostOut: 0.60},
		{ID: "o1", Name: "o1", CostIn: 15.00, CostOut: 60.00},
	}
	d.modelIdx = 0

	updated, _ := d.Update(tea.KeyMsg{Type: tea.KeyDown})
	if updated.modelIdx != 1 {
		t.Errorf("down should move to index 1, got %d", updated.modelIdx)
	}

	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	if updated.modelIdx != 2 {
		t.Errorf("down should move to index 2, got %d", updated.modelIdx)
	}

	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyDown})
	if updated.modelIdx != 0 {
		t.Errorf("down should wrap to 0, got %d", updated.modelIdx)
	}

	updated, _ = updated.Update(tea.KeyMsg{Type: tea.KeyUp})
	if updated.modelIdx != 2 {
		t.Errorf("up should wrap to last (2), got %d", updated.modelIdx)
	}
}

func TestSetupDialog_ModelSelectEmitsSavedMsg(t *testing.T) {
	d := NewSetupDialog()
	d.Show = true
	d.step = 2
	d.providerIdx = 0
	d.apiKey = "sk-abc"
	d.models = []providers.Model{
		{ID: "gpt-4o", Name: "GPT-4o", CostIn: 2.50, CostOut: 10.00},
		{ID: "gpt-4o-mini", Name: "GPT-4o Mini", CostIn: 0.15, CostOut: 0.60},
	}

	_, cmd := d.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter should return a command")
	}
	msg := cmd()
	saved, ok := msg.(SetupSavedMsg)
	if !ok {
		t.Fatalf("expected SetupSavedMsg, got %T", msg)
	}
	if saved.Provider != "openai" {
		t.Errorf("expected provider openai, got %s", saved.Provider)
	}
	if saved.Model != "gpt-4o" {
		t.Errorf("expected model gpt-4o, got %s", saved.Model)
	}
	if saved.APIKey != "sk-abc" {
		t.Errorf("expected apiKey sk-abc, got %s", saved.APIKey)
	}
}

func TestSetupDialog_EscapeAtStep0Cancels(t *testing.T) {
	d := NewSetupDialog()
	d.Show = true

	_, cmd := d.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("esc should return a command")
	}
	msg := cmd()
	if _, ok := msg.(CloseSetupDialogMsg); !ok {
		t.Errorf("expected CloseSetupDialogMsg, got %T", msg)
	}
}

func TestSetupDialog_EscapeGoesBack(t *testing.T) {
	d := NewSetupDialog()
	d.Show = true
	d.step = 1

	updated, cmd := d.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if updated.step != 0 {
		t.Errorf("esc from step 1 should go back to step 0, got step %d", updated.step)
	}
	if cmd != nil {
		t.Error("esc from step 1 should not emit a close command")
	}
}

func TestSetupDialog_ViewRendersProviderList(t *testing.T) {
	d := NewSetupDialog()
	d.Show = true

	v := d.View(80, 24)
	for _, p := range d.providers {
		if !strings.Contains(v, p) {
			t.Errorf("view should contain provider %q", p)
		}
	}
	if !strings.Contains(v, "Configure Provider") {
		t.Error("view should contain title")
	}
}

func TestSetupDialog_ViewRendersKeyStep(t *testing.T) {
	d := NewSetupDialog()
	d.Show = true
	d.step = 1
	d.apiKey = "testkey"

	v := d.View(80, 24)
	if !strings.Contains(v, "API Key") {
		t.Error("view should show API Key label")
	}
	if strings.Contains(v, "testkey") {
		t.Error("view should NOT show the raw API key when not editing")
	}
}

func TestSetupDialog_ViewRendersKeyEditing(t *testing.T) {
	d := NewSetupDialog()
	d.Show = true
	d.step = 1
	d.editingKey = true
	d.apiKey = "sk-ant-test-input-LAST5"

	v := d.View(80, 24)
	// While editing the key is still masked (last-5 visible) so screenshots /
	// shoulder-surfing don't leak the secret. The visible tail should appear.
	if !strings.Contains(v, "LAST5") {
		t.Error("view should show last 5 chars of key while editing")
	}
	if strings.Contains(v, "sk-ant-test-input") {
		t.Error("view must NOT show the full raw key while editing")
	}
}

func TestSetupDialog_ViewRendersModels(t *testing.T) {
	d := NewSetupDialog()
	d.Show = true
	d.step = 2
	d.models = []providers.Model{
		{ID: "gpt-4o", Name: "GPT-4o", CostIn: 2.50, CostOut: 10.00},
		{ID: "o1", Name: "o1", CostIn: 15.00, CostOut: 60.00},
	}

	v := d.View(80, 24)
	if !strings.Contains(v, "GPT-4o") {
		t.Error("view should contain model name")
	}
	if !strings.Contains(v, "$2.50") {
		t.Error("view should contain input cost")
	}
	if !strings.Contains(v, "$10.00") {
		t.Error("view should contain output cost")
	}
}

func TestSetupDialog_ViewHiddenWhenNotShown(t *testing.T) {
	d := NewSetupDialog()
	v := d.View(80, 24)
	if v != "" {
		t.Error("view should be empty when not shown")
	}
}

func TestSetupDialog_ShowResetsState(t *testing.T) {
	d := NewSetupDialog()
	d.step = 2
	d.apiKey = "old-key"
	d.editingKey = true

	updated, _ := d.Update(ShowSetupDialogMsg{})
	if updated.step != 0 {
		t.Errorf("ShowSetupDialogMsg should reset step to 0, got %d", updated.step)
	}
	if updated.apiKey != "" {
		t.Error("ShowSetupDialogMsg should clear apiKey")
	}
	if updated.editingKey {
		t.Error("ShowSetupDialogMsg should reset editingKey")
	}
	if !updated.Show {
		t.Error("ShowSetupDialogMsg should set Show=true")
	}
}

func TestSetupDialog_CustomProviderHasEmptyModels(t *testing.T) {
	d := NewSetupDialog()
	d.Show = true
	d.providerIdx = 6
	d.step = 2
	d.loadModelsForProvider("custom")

	if d.models != nil {
		t.Error("custom provider should have nil models")
	}
}

func TestSetupDialog_SavedMsgHasBaseURL(t *testing.T) {
	d := NewSetupDialog()
	d.providerIdx = 1
	d.apiKey = "key"
	d.models = []providers.Model{
		{ID: "claude-sonnet-4-20250514", Name: "Claude Sonnet 4", CostIn: 3.00, CostOut: 15.00},
	}

	msg := d.buildSavedMsg()
	if msg.BaseURL != "https://api.anthropic.com" {
		t.Errorf("expected anthropic base URL, got %s", msg.BaseURL)
	}
	if msg.Provider != "anthropic" {
		t.Errorf("expected provider anthropic, got %s", msg.Provider)
	}
}

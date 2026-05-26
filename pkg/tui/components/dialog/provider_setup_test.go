package dialog

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func keyRunes(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func TestProviderSetupDialog_OpenSeedsState(t *testing.T) {
	d := NewProviderSetupDialog()
	d.Open("anthropic", "claude-3-5-sonnet", "https://api.anthropic.com")
	if !d.Show {
		t.Fatal("dialog should be visible after Open")
	}
	if d.Provider != "anthropic" || d.Model != "claude-3-5-sonnet" || d.DefaultBaseURL != "https://api.anthropic.com" {
		t.Fatalf("seed mismatch: %+v", d)
	}
}

func TestProviderSetupDialog_TypingMaskedAndAdvances(t *testing.T) {
	d := NewProviderSetupDialog()
	d.Open("anthropic", "claude-3-5-sonnet", "https://api.anthropic.com")

	// Type a key.
	for _, r := range "sk-ant-123XYZ7Q9" {
		d, _ = d.Update(keyRunes(string(r)))
	}
	v := d.View(80, 24)
	if !strings.Contains(v, "YZ7Q9") {
		t.Errorf("view should expose last 5 chars, got: %s", v)
	}
	if strings.Contains(v, "sk-ant-123") {
		t.Error("view must NOT contain raw key prefix")
	}

	// Enter advances to endpoint step.
	d, _ = d.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if d.step != 1 {
		t.Fatalf("expected step 1 after enter, got %d", d.step)
	}
}

func TestProviderSetupDialog_TabTogglesCustomEndpoint(t *testing.T) {
	d := NewProviderSetupDialog()
	d.Open("openai", "gpt-4", "https://api.openai.com/v1")
	for _, r := range "secret" {
		d, _ = d.Update(keyRunes(string(r)))
	}
	d, _ = d.Update(tea.KeyMsg{Type: tea.KeyEnter})

	if d.useCustom {
		t.Fatal("custom should be off by default")
	}
	d, _ = d.Update(tea.KeyMsg{Type: tea.KeyTab})
	if !d.useCustom {
		t.Fatal("tab should toggle custom on")
	}
	d, _ = d.Update(tea.KeyMsg{Type: tea.KeyTab})
	if d.useCustom {
		t.Fatal("tab again should toggle custom off")
	}
}

func TestProviderSetupDialog_SubmitEmitsConfiguredMsg(t *testing.T) {
	d := NewProviderSetupDialog()
	d.Open("anthropic", "claude", "https://api.anthropic.com")
	for _, r := range "the-key" {
		d, _ = d.Update(keyRunes(string(r)))
	}
	// step 0 -> 1
	d, _ = d.Update(tea.KeyMsg{Type: tea.KeyEnter})
	// step 1 -> emit
	_, cmd := d.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected a tea.Cmd emitting ProviderConfiguredMsg")
	}
	msg := cmd()
	pc, ok := msg.(ProviderConfiguredMsg)
	if !ok {
		t.Fatalf("expected ProviderConfiguredMsg, got %T", msg)
	}
	if pc.Provider != "anthropic" || pc.Model != "claude" || pc.APIKey != "the-key" || pc.BaseURL != "https://api.anthropic.com" {
		t.Fatalf("unexpected payload: %+v", pc)
	}
}

func TestProviderSetupDialog_CustomURLSubmits(t *testing.T) {
	d := NewProviderSetupDialog()
	d.Open("openai", "gpt-4", "https://api.openai.com/v1")
	for _, r := range "k" {
		d, _ = d.Update(keyRunes(string(r)))
	}
	d, _ = d.Update(tea.KeyMsg{Type: tea.KeyEnter}) // -> step 1
	d, _ = d.Update(tea.KeyMsg{Type: tea.KeyTab})   // toggle custom
	for _, r := range "https://my.proxy/v1" {
		d, _ = d.Update(keyRunes(string(r)))
	}
	_, cmd := d.Update(tea.KeyMsg{Type: tea.KeyEnter})
	pc := cmd().(ProviderConfiguredMsg)
	if pc.BaseURL != "https://my.proxy/v1" {
		t.Fatalf("expected custom URL, got %q", pc.BaseURL)
	}
}

func TestProviderSetupDialog_EscEmitsClose(t *testing.T) {
	d := NewProviderSetupDialog()
	d.Open("anthropic", "claude", "https://api.anthropic.com")
	_, cmd := d.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("esc should emit a close msg")
	}
	if _, ok := cmd().(CloseProviderSetupMsg); !ok {
		t.Fatalf("expected CloseProviderSetupMsg")
	}
}

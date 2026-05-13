package page

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
)

func TestNewSetupPage_Defaults(t *testing.T) {
	m := NewSetupPage(nil)
	if m.focus != 0 {
		t.Errorf("focus = %d, want 0", m.focus)
	}
	if m.providerIdx != 0 {
		t.Errorf("providerIdx = %d, want 0", m.providerIdx)
	}
	if m.providers[m.providerIdx] != "openai" {
		t.Errorf("default provider = %s, want openai", m.providers[m.providerIdx])
	}
	if m.baseURL != "https://api.openai.com/v1" {
		t.Errorf("default baseURL = %s, want https://api.openai.com/v1", m.baseURL)
	}
	if len(m.models) == 0 {
		t.Error("models should not be empty")
	}
	if m.editing {
		t.Error("should not be editing initially")
	}
	if m.providerOpen {
		t.Error("provider dropdown should not be open initially")
	}
}

func TestSetupPage_BaseURLAutoFill(t *testing.T) {
	tests := []struct {
		provider string
		idx      int
		expected string
	}{
		{"openai", 0, "https://api.openai.com/v1"},
		{"anthropic", 1, "https://api.anthropic.com"},
		{"gemini", 2, "https://generativelanguage.googleapis.com/v1beta"},
		{"deepseek", 3, "https://api.deepseek.com/v1"},
		{"ollama", 4, "http://localhost:11434"},
		{"openrouter", 5, "https://openrouter.ai/api/v1"},
	}

	for _, tc := range tests {
		t.Run(tc.provider, func(t *testing.T) {
			m := NewSetupPage(nil)
			m.providerIdx = tc.idx
			m.onProviderChange()
			if m.baseURL != tc.expected {
				t.Errorf("baseURL for %s = %s, want %s", tc.provider, m.baseURL, tc.expected)
			}
		})
	}
}

func TestSetupPage_CustomProviderNoDefaultURL(t *testing.T) {
	m := NewSetupPage(nil)
	m.providerIdx = 6
	m.onProviderChange()
	if m.baseURL != "https://api.openai.com/v1" {
		t.Errorf("custom provider should keep previous baseURL, got %s", m.baseURL)
	}
}

func TestSetupPage_ProviderCycling(t *testing.T) {
	m := NewSetupPage(nil)
	if m.SelectedProvider() != "openai" {
		t.Errorf("SelectedProvider() = %s, want openai", m.SelectedProvider())
	}

	providers := []string{"openai", "anthropic", "gemini", "deepseek", "ollama", "openrouter", "custom"}
	for i, expected := range providers {
		m.providerIdx = i
		if m.SelectedProvider() != expected {
			t.Errorf("providerIdx=%d: got %s, want %s", i, m.SelectedProvider(), expected)
		}
	}
}

func TestSetupPage_FetchModelsByProvider(t *testing.T) {
	tests := []struct {
		name     string
		idx      int
		minCount int
	}{
		{"openai", 0, 3},
		{"anthropic", 1, 2},
		{"gemini", 2, 2},
		{"deepseek", 3, 1},
		{"ollama", 4, 2},
		{"openrouter", 5, 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewSetupPage(nil)
			m.providerIdx = tt.idx
			m.onProviderChange()
			if !m.fetching {
				t.Error("should be fetching after provider change")
			}
			m.fetching = false
			m.models = fetchModelsForProvider(m.providers[m.providerIdx])
			if len(m.models) < tt.minCount {
				t.Errorf("got %d models for %s, want at least %d", len(m.models), tt.name, tt.minCount)
			}
		})
	}
}

func TestSetupPage_ModelSelection(t *testing.T) {
	var m SetupPage
	m = NewSetupPage(nil)
	m.focus = 3
	if m.cursor != 0 {
		t.Errorf("initial cursor = %d, want 0", m.cursor)
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.cursor != 1 {
		t.Errorf("cursor after down = %d, want 1", m.cursor)
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.cursor != 2 {
		t.Errorf("cursor after 2 down = %d, want 2", m.cursor)
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.cursor != 1 {
		t.Errorf("cursor after up = %d, want 1", m.cursor)
	}

	// Can't go above 0
	m.cursor = 0
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.cursor != 0 {
		t.Errorf("cursor should stay at 0, got %d", m.cursor)
	}

	// Can't go below last
	m.cursor = len(m.models) - 1
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.cursor != len(m.models)-1 {
		t.Errorf("cursor should stay at last, got %d", m.cursor)
	}
}

func TestSetupPage_FocusSwitching(t *testing.T) {
	var m SetupPage
	m = NewSetupPage(nil)
	if m.focus != 0 {
		t.Errorf("initial focus = %d, want 0", m.focus)
	}

	// Tab forward
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if m.focus != 1 {
		t.Errorf("focus after tab = %d, want 1", m.focus)
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if m.focus != 2 {
		t.Errorf("focus after 2 tab = %d, want 2", m.focus)
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if m.focus != 3 {
		t.Errorf("focus after 3 tab = %d, want 3", m.focus)
	}

	// Wraps around
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if m.focus != 0 {
		t.Errorf("focus after wrap = %d, want 0", m.focus)
	}

	// Right arrow
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if m.focus != 1 {
		t.Errorf("focus after right = %d, want 1", m.focus)
	}

	// Left arrow
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if m.focus != 0 {
		t.Errorf("focus after left = %d, want 0", m.focus)
	}

	// Left at 0 wraps to 3
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if m.focus != 3 {
		t.Errorf("focus after left wrap = %d, want 3", m.focus)
	}
}

func TestSetupPage_OllamaSkipsAPIKey(t *testing.T) {
	var m SetupPage
	m = NewSetupPage(nil)
	m.providerIdx = 4 // ollama
	m.focus = 1

	if m.SelectedProvider() != "ollama" {
		t.Errorf("provider = %s, want ollama", m.SelectedProvider())
	}

	// Press Enter on API key field — should NOT start editing
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.editing {
		t.Error("ollama should not allow editing API key")
	}
}

func TestSetupPage_APIKeyEditing(t *testing.T) {
	var m SetupPage
	m = NewSetupPage(nil)
	m.focus = 1

	// Enter starts editing
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !m.editing {
		t.Error("Enter should start editing API key")
	}

	// Type characters
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("sk-")})
	if m.apiKey != "sk-" {
		t.Errorf("apiKey = %q, want %q", m.apiKey, "sk-")
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("test123")})
	if m.apiKey != "sk-test123" {
		t.Errorf("apiKey = %q, want %q", m.apiKey, "sk-test123")
	}

	// Backspace
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if m.apiKey != "sk-test12" {
		t.Errorf("apiKey after backspace = %q, want %q", m.apiKey, "sk-test12")
	}

	// Enter stops editing
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.editing {
		t.Error("Enter should stop editing")
	}

	// Esc stops editing
	m.focus = 1
	m.editing = true
	m.editingField = 1
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.editing {
		t.Error("Esc should stop editing")
	}
}

func TestSetupPage_BaseURLEditing(t *testing.T) {
	var m SetupPage
	m = NewSetupPage(nil)
	m.focus = 2

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !m.editing || m.editingField != 2 {
		t.Error("Enter should start editing base URL")
	}

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/custom")})
	if m.baseURL != "https://api.openai.com/v1/custom" {
		t.Errorf("baseURL = %q, want to contain /custom", m.baseURL)
	}
}

func TestSetupPage_Complete(t *testing.T) {
	cfg := config.Default()
	var m SetupPage
	m = NewSetupPage(cfg)
	m.focus = 3
	m.cursor = 0

	if m.Done() {
		t.Error("Done should be false initially")
	}

	m, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !m.Done() {
		t.Error("Done should be true after complete")
	}

	if cmd == nil {
		t.Error("should have a complete cmd")
	}

	msg := cmd()
	completeMsg, ok := msg.(SetupCompleteMsg)
	if !ok {
		t.Fatalf("expected SetupCompleteMsg, got %T", msg)
	}
	if completeMsg.Provider != "openai" {
		t.Errorf("provider = %s, want openai", completeMsg.Provider)
	}
	if completeMsg.Model != "gpt-4o" {
		t.Errorf("model = %s, want gpt-4o", completeMsg.Model)
	}
	if completeMsg.Config != cfg {
		t.Error("config should be passed through")
	}
}

func TestSetupPage_ProviderDropdown(t *testing.T) {
	var m SetupPage
	m = NewSetupPage(nil)

	// Open dropdown
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !m.providerOpen {
		t.Error("Enter on provider should open dropdown")
	}

	// Navigate down in dropdown
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.providerIdx != 1 {
		t.Errorf("providerIdx after down = %d, want 1", m.providerIdx)
	}
	if m.providers[m.providerIdx] != "anthropic" {
		t.Errorf("provider after down = %s, want anthropic", m.providers[m.providerIdx])
	}

	// Navigate up
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.providerIdx != 0 {
		t.Errorf("providerIdx after up = %d, want 0", m.providerIdx)
	}

	// Can't go above 0 in dropdown
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if m.providerIdx != 0 {
		t.Errorf("providerIdx should stay at 0, got %d", m.providerIdx)
	}

	// Can't go below last
	m.providerIdx = len(m.providers) - 1
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if m.providerIdx != len(m.providers)-1 {
		t.Errorf("providerIdx should stay at last, got %d", m.providerIdx)
	}

	// Select with Enter closes dropdown
	m.providerIdx = 2
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.providerOpen {
		t.Error("Enter on dropdown item should close dropdown")
	}
	if m.providers[m.providerIdx] != "gemini" {
		t.Errorf("selected provider = %s, want gemini", m.providers[m.providerIdx])
	}

	// Esc closes dropdown without changing
	m.providerOpen = true
	m.providerIdx = 0
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.providerOpen {
		t.Error("Esc should close dropdown")
	}
}

func TestSetupPage_SelectedModel(t *testing.T) {
	m := NewSetupPage(nil)
	if m.SelectedModel() != "gpt-4o" {
		t.Errorf("SelectedModel() = %s, want gpt-4o", m.SelectedModel())
	}

	m.cursor = 2
	if m.SelectedModel() != "o1" {
		t.Errorf("SelectedModel() at cursor 2 = %s, want o1", m.SelectedModel())
	}
}

func TestSetupPage_SaveConfig(t *testing.T) {
	m := NewSetupPage(nil)
	m.apiKey = "sk-test-key"
	m.providerIdx = 0
	m.cursor = 0

	cfg := config.Default()
	cfg.Providers = []config.ProviderConfig{}

	err := m.SaveConfig(cfg)
	if err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	if cfg.Agent.DefaultProvider != "openai" {
		t.Errorf("default provider = %s, want openai", cfg.Agent.DefaultProvider)
	}
	if cfg.Agent.DefaultModel != "gpt-4o" {
		t.Errorf("default model = %s, want gpt-4o", cfg.Agent.DefaultModel)
	}

	found := false
	for _, p := range cfg.Providers {
		if p.Name == "openai" {
			found = true
			if p.APIKey != "sk-test-key" {
				t.Errorf("provider apiKey = %s, want sk-test-key", p.APIKey)
			}
			if p.BaseURL != "https://api.openai.com/v1" {
				t.Errorf("provider baseURL = %s, want https://api.openai.com/v1", p.BaseURL)
			}
			break
		}
	}
	if !found {
		t.Error("openai provider not found in config")
	}
}

func TestSetupPage_SaveConfigUpdatesExisting(t *testing.T) {
	m := NewSetupPage(nil)
	m.apiKey = "sk-new-key"
	m.providerIdx = 0
	m.cursor = 1

	cfg := config.Default()
	cfg.Providers = []config.ProviderConfig{
		{Name: "openai", Type: "openai", APIKey: "sk-old-key", BaseURL: "https://old.example.com"},
	}

	err := m.SaveConfig(cfg)
	if err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	if len(cfg.Providers) != 1 {
		t.Fatalf("expected 1 provider, got %d", len(cfg.Providers))
	}
	if cfg.Providers[0].APIKey != "sk-new-key" {
		t.Errorf("apiKey = %s, want sk-new-key", cfg.Providers[0].APIKey)
	}
}

func TestSetupPage_MaskKey(t *testing.T) {
	tests := []struct {
		key      string
		show     bool
		expected string
	}{
		{"", false, ""},
		{"ab", false, "**"},
		{"abcd", false, "****"},
		{"sk-abcdefgh", false, "*******efgh"},
		{"sk-abcdefgh", true, "sk-abcdefgh"},
	}

	for _, tc := range tests {
		result := maskKey(tc.key, tc.show)
		if result != tc.expected {
			t.Errorf("maskKey(%q, %v) = %q, want %q", tc.key, tc.show, result, tc.expected)
		}
	}
}

func TestSetupPage_APIKeyAccessors(t *testing.T) {
	m := NewSetupPage(nil)
	m.apiKey = "sk-mykey"
	if m.APIKey() != "sk-mykey" {
		t.Errorf("APIKey() = %s, want sk-mykey", m.APIKey())
	}
}

func TestSetupPage_BaseURLAccessor(t *testing.T) {
	m := NewSetupPage(nil)
	m.baseURL = "https://custom.example.com"
	if m.BaseURL() != "https://custom.example.com" {
		t.Errorf("BaseURL() = %s, want https://custom.example.com", m.BaseURL())
	}
}

func TestSetupPage_EmptyView(t *testing.T) {
	m := NewSetupPage(nil)
	m.width = 0
	if m.View() != "" {
		t.Error("View should be empty when width is 0")
	}
}

func TestSetupPage_Init(t *testing.T) {
	m := NewSetupPage(nil)
	cmd := m.Init()
	if cmd == nil {
		t.Error("Init should return a command")
	}
}

func TestSetupPage_LeftRightAtBounds(t *testing.T) {
	var m SetupPage
	m = NewSetupPage(nil)
	m.focus = 0
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if m.focus != 1 {
		t.Errorf("right from 0: got %d, want 1", m.focus)
	}
	m.focus = 3
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if m.focus != 0 {
		t.Errorf("right from 3: got %d, want 0", m.focus)
	}
	m.focus = 0
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if m.focus != 3 {
		t.Errorf("left from 0: got %d, want 3", m.focus)
	}
}

func TestSetupPage_RunesNotEditing(t *testing.T) {
	m := NewSetupPage(nil)
	m.focus = 0

	// When not editing, runes should not change anything
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if updated.editing {
		t.Error("should not be editing")
	}
}

func TestSetupPage_EscFocus(t *testing.T) {
	var m SetupPage
	m = NewSetupPage(nil)
	m.focus = 3 // models

	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.focus != 0 {
		t.Errorf("Esc should move focus from models to provider, got %d", m.focus)
	}

	m.focus = 2
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if m.focus != 0 {
		t.Errorf("Esc should move focus to provider, got %d", m.focus)
	}
}

func TestSetupPage_FocusCycleRight(t *testing.T) {
	var m SetupPage
	m = NewSetupPage(nil)
	m.focus = 3
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if m.focus != 0 {
		t.Errorf("right from 3: got %d, want 0", m.focus)
	}
	m.focus = 2
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	if m.focus != 3 {
		t.Errorf("right from 2: got %d, want 3", m.focus)
	}
}

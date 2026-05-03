package onboarding

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Sahaj-Tech-ltd/ethos/internal/config"
)

func tempHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	return dir
}

func TestHasOnboarded_FirstRun(t *testing.T) {
	home := tempHome(t)
	if HasOnboarded(home) {
		t.Error("fresh HOME should not look onboarded")
	}
}

func TestSkip_WritesMarker(t *testing.T) {
	home := tempHome(t)
	m := New(&config.Config{})
	m.homeDir = home
	cmd := m.skip()
	if cmd == nil {
		t.Fatal("skip should return a cmd")
	}
	msg := cmd()
	cm, ok := msg.(CompleteMsg)
	if !ok || !cm.Skipped {
		t.Fatalf("expected skipped CompleteMsg, got %+v", msg)
	}
	if !HasOnboarded(home) {
		t.Error("marker file should exist after skip")
	}
	body, err := os.ReadFile(MarkerPath(home))
	if err != nil {
		t.Fatal(err)
	}
	var p markerPayload
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatal(err)
	}
	if !p.Skipped {
		t.Error("marker should record skipped=true")
	}
}

func TestComplete_WritesMarkerAndConfig(t *testing.T) {
	home := tempHome(t)
	cfg := &config.Config{Version: config.CurrentVersion}
	m := New(cfg)
	m.homeDir = home
	cmd := m.complete()
	msg := cmd()
	cm, ok := msg.(CompleteMsg)
	if !ok || cm.Skipped {
		t.Fatalf("expected non-skipped CompleteMsg, got %+v", msg)
	}
	if !HasOnboarded(home) {
		t.Error("marker file should exist after complete")
	}
	body, _ := os.ReadFile(MarkerPath(home))
	if strings.Contains(string(body), `"skipped":true`) {
		t.Errorf("complete should not record a skip: %s", body)
	}
}

func TestStepTransitions_Welcome_To_Model_To_APIKey(t *testing.T) {
	home := tempHome(t)
	m := New(&config.Config{})
	m.homeDir = home
	if m.Step() != StepWelcome {
		t.Fatalf("start step = %v, want welcome", m.Step())
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.Step() != StepModel {
		t.Fatalf("after enter = %v, want model", m.Step())
	}
	// Move down once to pick the second provider.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.Step() != StepAPIKey {
		t.Fatalf("after enter on model = %v, want apikey", m.Step())
	}
	if m.cfg.Agent.DefaultProvider == "" {
		t.Error("provider should be set on model step")
	}
}

func TestAPIKey_TypeAndAdvance(t *testing.T) {
	home := tempHome(t)
	m := New(&config.Config{})
	m.homeDir = home
	m.step = StepAPIKey
	m.cfg.Agent.DefaultProvider = "anthropic"
	for _, r := range "sk-test123" {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	if m.apiKey != "sk-test123" {
		t.Errorf("apiKey = %q", m.apiKey)
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.Step() != StepOptional {
		t.Errorf("after enter on key = %v, want optional", m.Step())
	}
	// Ensure provider entry was upserted.
	if len(m.cfg.Providers) != 1 || m.cfg.Providers[0].APIKey != "sk-test123" {
		t.Errorf("provider not upserted: %+v", m.cfg.Providers)
	}
}

func TestOptional_TogglesAndFinish(t *testing.T) {
	home := tempHome(t)
	m := New(&config.Config{})
	m.homeDir = home
	m.step = StepOptional
	// Toggle sync (cursor 0, space).
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" ")})
	if !m.enableSync {
		t.Error("space on cursor 0 should toggle sync on")
	}
	// Move to "all set" and press enter.
	for range 4 {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	mm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if mm.Step() != StepDone {
		t.Errorf("after enter on next = %v, want done", mm.Step())
	}
	if cmd != nil {
		// Done step doesn't emit until a key press.
	}
	if mm.cfg.Sync.Backend != "file" {
		t.Errorf("sync backend should be set to file: %q", mm.cfg.Sync.Backend)
	}
}

func TestEsc_AnyStep_Skips(t *testing.T) {
	home := tempHome(t)
	for _, step := range []Step{StepWelcome, StepModel, StepAPIKey, StepOptional} {
		m := New(&config.Config{})
		m.homeDir = home
		m.step = step
		_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
		if cmd == nil {
			t.Errorf("esc on step %v should produce a cmd", step)
			continue
		}
		msg := cmd()
		if cm, ok := msg.(CompleteMsg); !ok || !cm.Skipped {
			t.Errorf("esc on step %v: got %+v", step, msg)
		}
	}
}

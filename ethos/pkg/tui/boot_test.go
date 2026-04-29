package tui

import (
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestBoot_LoadSoulMD(t *testing.T) {
	dir := t.TempDir()
	soulDir := filepath.Join(dir, ".ethos", "memories")
	os.MkdirAll(soulDir, 0755)
	soulFile := filepath.Join(soulDir, "soul.md")
	os.WriteFile(soulFile, []byte("test soul content"), 0644)
	origHome := os.Getenv("HOME")
	os.Setenv("HOME", dir)
	defer os.Setenv("HOME", origHome)

	cmd := LoadBootData(nil)
	msg := cmd()
	boot, ok := msg.(BootCompleteMsg)
	if !ok {
		t.Fatal("wrong message type")
	}
	if boot.SoulMD != "test soul content" {
		t.Errorf("soul md mismatch: got %q", boot.SoulMD)
	}
}

func TestBoot_LoadFunFact(t *testing.T) {
	cmd := LoadBootData(nil)
	msg := cmd()
	boot, ok := msg.(BootCompleteMsg)
	if !ok {
		t.Fatal("wrong message type")
	}
	if boot.FunFact == "" {
		t.Error("should have fun fact")
	}
}

func TestBoot_BootComplete(t *testing.T) {
	b := NewBootModel()
	b.Update(BootCompleteMsg{FunFact: "test fact", SoulMD: "test soul"})
	if b.funFact != "test fact" {
		t.Error("fun fact not set")
	}
	if b.soulMD != "test soul" {
		t.Error("soul md not set")
	}
}

func TestBoot_BootView(t *testing.T) {
	b := NewBootModel()
	b.funFact = "test fact"
	b.visible = true
	v := b.View()
	if !containsStr(v, "E T H O S") {
		t.Error("should show logo")
	}
	if !containsStr(v, "test fact") {
		t.Error("should show fun fact")
	}
}

func TestBoot_BootFade(t *testing.T) {
	b := NewBootModel()
	b.visible = true
	b.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	if b.visible {
		t.Error("should be hidden after keypress")
	}
}

func TestBoot_EmptySoulMD(t *testing.T) {
	dir := t.TempDir()
	os.Setenv("HOME", dir)
	defer os.Unsetenv("HOME")

	cmd := LoadBootData(nil)
	msg := cmd()
	boot := msg.(BootCompleteMsg)
	if boot.SoulMD != "" {
		t.Error("should be empty without file")
	}
}

func TestBoot_FirstRun(t *testing.T) {
	b := NewBootModel()
	b.SetFirstRun(true)
	b.visible = true
	v := b.View()
	if !containsStr(v, "finally awake") {
		t.Error("should show first run message")
	}
}

func TestBoot_PersonalityLoaded(t *testing.T) {
	cmd := LoadBootData(nil)
	msg := cmd()
	boot := msg.(BootCompleteMsg)
	if boot.FunFact == "" {
		t.Error("should have fun fact")
	}
}

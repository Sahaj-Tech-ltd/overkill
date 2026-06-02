package personality

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadIdentity_EmbeddedDefault(t *testing.T) {
	// Resolve fully embedded by passing a HOME that has no identity.toml.
	t.Setenv("HOME", t.TempDir())
	id, err := LoadIdentity()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if id == nil {
		t.Fatal("identity nil")
	}
	if !strings.Contains(id.WhoIAm, "Overkill") {
		t.Errorf("default identity missing 'Overkill': %q", id.WhoIAm)
	}
	if !strings.Contains(id.Roastability, "Feedback") {
		t.Errorf("default identity missing Roastability content: %q", id.Roastability)
	}
}

func TestLoadIdentity_OverridePreferred(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	overkillDir := filepath.Join(home, ".overkill")
	if err := os.MkdirAll(overkillDir, 0o750); err != nil {
		t.Fatal(err)
	}
	override := `[identity]
who_i_am = "Custom Voice"
how_i_talk = "Robotically."
what_i_believe = "Forks are real."
self_awareness = "I am the override."
roastability = "Roast on."
`
	if err := os.WriteFile(filepath.Join(overkillDir, "identity.toml"), []byte(override), 0o644); err != nil {
		t.Fatal(err)
	}
	id, err := LoadIdentity()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if id.WhoIAm != "Custom Voice" {
		t.Errorf("override not honored: %q", id.WhoIAm)
	}
}

func TestLoadIdentity_MalformedOverrideFallsBackToDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	overkillDir := filepath.Join(home, ".overkill")
	_ = os.MkdirAll(overkillDir, 0o750)
	bad := `this = is = not = valid = toml`
	if err := os.WriteFile(filepath.Join(overkillDir, "identity.toml"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	id, err := LoadIdentity()
	if err == nil {
		t.Error("expected error to surface so caller can warn")
	}
	if id == nil {
		t.Fatal("identity should fall back to embedded default, got nil")
	}
	if !strings.Contains(id.WhoIAm, "Overkill") {
		t.Errorf("malformed override should fall back to default Overkill identity, got %q", id.WhoIAm)
	}
}

func TestIdentity_SystemPromptBlockIncludesAllFields(t *testing.T) {
	id := &Identity{
		WhoIAm:        "Overkill.",
		HowITalk:      "Direct.",
		WhatIBelieve:  "Competence.",
		SelfAwareness: "I'm an AI.",
		Roastability:  "Take the L.",
	}
	block := id.SystemPromptBlock()
	for _, header := range []string{"Who I am", "How I talk", "What I believe", "Self-awareness", "Roastability"} {
		if !strings.Contains(block, header) {
			t.Errorf("system prompt block missing header %q:\n%s", header, block)
		}
	}
	if !strings.Contains(block, "Baseline identity") {
		t.Error("system prompt should start with 'Baseline identity' anchor")
	}
}

func TestIdentity_SystemPromptBlockSkipsEmptyFields(t *testing.T) {
	id := &Identity{
		WhoIAm:   "Overkill.",
		HowITalk: "", // empty — should be skipped
	}
	block := id.SystemPromptBlock()
	if !strings.Contains(block, "Who I am") {
		t.Error("non-empty field missing")
	}
	if strings.Contains(block, "How I talk") {
		t.Error("empty field should be skipped, not rendered with blank body")
	}
}

func TestIdentity_SystemPromptBlockNilReturnsEmpty(t *testing.T) {
	var id *Identity
	if got := id.SystemPromptBlock(); got != "" {
		t.Errorf("nil identity should return empty, got %q", got)
	}
}

func TestIdentity_SystemPromptBlockFlattensMultiline(t *testing.T) {
	id := &Identity{
		WhoIAm: "Line one.\n  Line two.\n  Line three.",
	}
	block := id.SystemPromptBlock()
	// Each field is a single line in the prompt block — multi-line
	// source TOML gets collapsed so the prompt stays compact.
	if strings.Count(block, "Line one") != 1 {
		t.Errorf("multi-line field not flattened: %q", block)
	}
	if !strings.Contains(block, "Line one. Line two. Line three.") {
		t.Errorf("flattened text missing or malformed: %q", block)
	}
}

func TestIdentity_DisplayPreservesProse(t *testing.T) {
	id := &Identity{
		WhoIAm:   "Overkill.",
		HowITalk: "Direct.",
	}
	out := id.Display()
	if !strings.Contains(out, "Overkill — baseline identity") {
		t.Errorf("display header missing: %q", out)
	}
	if !strings.Contains(out, "Who I am") || !strings.Contains(out, "Overkill.") {
		t.Errorf("display field missing: %q", out)
	}
}

func TestIdentity_DisplayNilReturnsPlaceholder(t *testing.T) {
	var id *Identity
	if got := id.Display(); !strings.Contains(got, "no identity") {
		t.Errorf("nil identity Display should explain: %q", got)
	}
}

func TestPersonality_NewLoadsIdentityOnEveryLevel(t *testing.T) {
	// Each level constructs a Personality; baseline identity must be
	// non-nil regardless. This is the §4.16 contract.
	for _, level := range []Level{LevelOff, LevelSubtle, LevelWitty, LevelFull} {
		p := New(Config{Level: level})
		if p.Identity() == nil {
			t.Errorf("level %v: identity should always load, got nil", level)
		}
	}
}

func TestPersonality_IdentityInjectedBeforeBasePrompt(t *testing.T) {
	p := New(Config{Level: LevelSubtle})
	got := p.InjectPersonality("ORIGINAL PROMPT")

	idIdx := strings.Index(got, "Baseline identity")
	promptIdx := strings.Index(got, "ORIGINAL PROMPT")
	if idIdx == -1 {
		t.Fatal("identity block missing from injected prompt")
	}
	if promptIdx == -1 {
		t.Fatal("original prompt missing from injected output")
	}
	if idIdx >= promptIdx {
		t.Errorf("identity should appear BEFORE base prompt; idIdx=%d promptIdx=%d", idIdx, promptIdx)
	}
}

func TestPersonality_NilIdentityIsSafe(t *testing.T) {
	// Defensive: even if identity load fails entirely, Personality
	// methods shouldn't panic. We can't easily force LoadIdentity to
	// return nil in a test, so we construct a Personality and zero
	// out the identity manually to simulate the failure mode.
	p := New(Config{Level: LevelFull})
	p.identity = nil
	got := p.InjectPersonality("hello")
	if !strings.Contains(got, "hello") {
		t.Errorf("nil identity must not eat the base prompt: %q", got)
	}
}

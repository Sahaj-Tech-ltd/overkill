package models

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// writeTOML writes a TOML file at root/relpath, creating intermediate
// directories as needed. Test helper.
func writeTOML(t *testing.T, root, relpath, body string) {
	t.Helper()
	full := filepath.Join(root, relpath)
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoad_EmptyRootIsOK(t *testing.T) {
	c, err := Load("")
	if err != nil {
		t.Errorf("empty root should not error, got %v", err)
	}
	if len(c.List()) != 0 {
		t.Errorf("empty root should produce empty catalog, got %d", len(c.List()))
	}
}

func TestLoad_NonexistentRootIsOK(t *testing.T) {
	c, err := Load("/path/that/definitely/does/not/exist")
	if err != nil {
		t.Errorf("missing root should not error, got %v", err)
	}
	if len(c.List()) != 0 {
		t.Error("missing root should produce empty catalog")
	}
}

func TestLoad_FilenameDerivesID(t *testing.T) {
	root := t.TempDir()
	writeTOML(t, root, "openai/gpt-5.toml", `
family = "gpt-5"
context_window = 128000
[cost]
input = 5.0
output = 15.0
`)
	c, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	m, err := c.Get("openai/gpt-5")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if m.ID != "openai/gpt-5" {
		t.Errorf("ID = %q, want openai/gpt-5", m.ID)
	}
	if m.Family != "gpt-5" {
		t.Errorf("family = %q", m.Family)
	}
}

func TestLoad_ExtendsInheritance(t *testing.T) {
	root := t.TempDir()
	writeTOML(t, root, "openai/gpt-5.toml", `
family = "gpt-5"
context_window = 128000
display_name = "GPT-5"
[capabilities]
reasoning = true
tool_call = true
[cost]
input = 5.0
output = 15.0
`)
	writeTOML(t, root, "openrouter/gpt-5.toml", `
[extends]
from = "openai/gpt-5"
[cost]
input = 6.0
output = 18.0
`)
	c, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	w, err := c.Get("openrouter/gpt-5")
	if err != nil {
		t.Fatalf("Get wrapper: %v", err)
	}
	if w.Family != "gpt-5" {
		t.Errorf("wrapper should inherit family, got %q", w.Family)
	}
	if w.ContextWindow != 128000 {
		t.Errorf("wrapper should inherit context_window, got %d", w.ContextWindow)
	}
	if w.DisplayName != "GPT-5" {
		t.Errorf("wrapper should inherit display_name, got %q", w.DisplayName)
	}
	if !w.Capabilities.Reasoning || !w.Capabilities.ToolCall {
		t.Errorf("wrapper should inherit capabilities, got %+v", w.Capabilities)
	}
	if w.Cost.Input != 6.0 || w.Cost.Output != 18.0 {
		t.Errorf("wrapper should KEEP its overridden cost, got %+v", w.Cost)
	}
}

func TestLoad_ListFamily(t *testing.T) {
	root := t.TempDir()
	writeTOML(t, root, "anthropic/claude-opus-4.toml", `family = "claude-opus"
context_window = 200000
[cost]
input = 15
output = 75
`)
	writeTOML(t, root, "anthropic/claude-opus-5.toml", `family = "claude-opus"
context_window = 200000
[cost]
input = 18
output = 90
`)
	writeTOML(t, root, "anthropic/claude-haiku-4.toml", `family = "claude-haiku"
context_window = 200000
[cost]
input = 0.25
output = 1.25
`)
	c, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	opus := c.ListFamily("claude-opus")
	if len(opus) != 2 {
		t.Errorf("expected 2 opus models, got %d", len(opus))
	}
	if c.ListFamily("nope") != nil && len(c.ListFamily("nope")) > 0 {
		t.Errorf("unknown family should be empty")
	}
}

func TestLoad_CheapestInFamily(t *testing.T) {
	root := t.TempDir()
	writeTOML(t, root, "anthropic/claude-opus-4.toml", `family = "claude-opus"
context_window = 200000
[cost]
input = 15
output = 75
`)
	writeTOML(t, root, "anthropic/claude-opus-5.toml", `family = "claude-opus"
context_window = 200000
[cost]
input = 18
output = 90
`)
	c, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cheap, err := c.CheapestInFamily("claude-opus")
	if err != nil {
		t.Fatal(err)
	}
	if cheap.ID != "anthropic/claude-opus-4" {
		t.Errorf("cheapest should be opus-4 (output=75), got %s (output=%.0f)",
			cheap.ID, cheap.Cost.Output)
	}
}

func TestLoad_CheapestSkipsDeprecated(t *testing.T) {
	root := t.TempDir()
	writeTOML(t, root, "anthropic/claude-opus-4.toml", `family = "claude-opus"
context_window = 200000
deprecated = true
[cost]
input = 15
output = 50
`)
	writeTOML(t, root, "anthropic/claude-opus-5.toml", `family = "claude-opus"
context_window = 200000
[cost]
input = 18
output = 90
`)
	c, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cheap, err := c.CheapestInFamily("claude-opus")
	if err != nil {
		t.Fatal(err)
	}
	if cheap.ID != "anthropic/claude-opus-5" {
		t.Errorf("deprecated should be skipped; got %s", cheap.ID)
	}
}

func TestLoad_ListWithCapability(t *testing.T) {
	root := t.TempDir()
	writeTOML(t, root, "x/r.toml", `family = "x"
context_window = 1000
[capabilities]
reasoning = true
tool_call = true
[cost]
input = 1
output = 1
`)
	writeTOML(t, root, "x/no.toml", `family = "x"
context_window = 1000
[cost]
input = 1
output = 1
`)
	c, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := c.ListWithCapability(Capabilities{Reasoning: true})
	if len(got) != 1 || got[0].ID != "x/r" {
		t.Errorf("expected only x/r, got %v", got)
	}
}

func TestLoad_MissingRequiredFields(t *testing.T) {
	root := t.TempDir()
	writeTOML(t, root, "broken.toml", `
[cost]
input = 1
output = 1
`)
	_, err := Load(root)
	if err == nil {
		t.Fatal("expected validation error for missing family + context_window")
	}
}

func TestLoad_UnknownExtendsBase(t *testing.T) {
	root := t.TempDir()
	writeTOML(t, root, "wrapper.toml", `
[extends]
from = "nonexistent/base"
`)
	_, err := Load(root)
	if err == nil {
		t.Fatal("expected error for unknown extends base")
	}
}

func TestCatalog_GetUnknown(t *testing.T) {
	c, _ := Load("")
	_, err := c.Get("missing")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown ID should return ErrNotFound, got %v", err)
	}
}

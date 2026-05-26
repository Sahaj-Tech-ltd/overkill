package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeYAML(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadHierarchical_UserOverridesSystem(t *testing.T) {
	dir := t.TempDir()
	sysFile := filepath.Join(dir, "system.yaml")
	userFile := filepath.Join(dir, "user.yaml")

	writeYAML(t, sysFile, `
schema_version: 1
basic:
  model: claude-sonnet-4-6
  vim_mode: false
advanced:
  persona:
    tone: terse
`)
	writeYAML(t, userFile, `
schema_version: 1
basic:
  model: claude-opus-4-7
`)

	out, err := LoadHierarchical(LayerSources{SystemFile: sysFile, UserFile: userFile})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if out.Basic.Model != "claude-opus-4-7" {
		t.Errorf("Model = %q, want user-layer override claude-opus-4-7", out.Basic.Model)
	}
	if out.Advanced.Persona.Tone != "terse" {
		t.Errorf("Persona.Tone = %q, want system-layer terse", out.Advanced.Persona.Tone)
	}
}

func TestLoadHierarchical_EnforcedWinsLast(t *testing.T) {
	dir := t.TempDir()
	userFile := filepath.Join(dir, "user.yaml")
	enforcedFile := filepath.Join(dir, "enforced.yaml")

	// User wants scanners off.
	writeYAML(t, userFile, `
schema_version: 1
advanced:
  scanners:
    command:
      enabled: false
    injection:
      enabled: false
`)
	// Enforced layer forces them on.
	writeYAML(t, enforcedFile, `
schema_version: 1
advanced:
  scanners:
    command:
      enabled: true
    injection:
      enabled: true
`)

	out, err := LoadHierarchical(LayerSources{UserFile: userFile, EnforcedFile: enforcedFile})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !out.Advanced.Scanners.Command.Enabled {
		t.Error("enforced layer should force command scanner on")
	}
	if !out.Advanced.Scanners.Injection.Enabled {
		t.Error("enforced layer should force injection scanner on")
	}
}

func TestLoadHierarchical_WorkspaceOverridesUser(t *testing.T) {
	dir := t.TempDir()
	userFile := filepath.Join(dir, "user.yaml")
	wsFile := filepath.Join(dir, "workspace.yaml")

	writeYAML(t, userFile, `
schema_version: 1
basic:
  model: claude-opus-4-7
advanced:
  persona:
    style: senior
`)
	writeYAML(t, wsFile, `
schema_version: 1
advanced:
  persona:
    style: tutor
`)

	out, err := LoadHierarchical(LayerSources{UserFile: userFile, WorkspaceFile: wsFile})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if out.Basic.Model != "claude-opus-4-7" {
		t.Errorf("Model should inherit from user: got %q", out.Basic.Model)
	}
	if out.Advanced.Persona.Style != "tutor" {
		t.Errorf("Persona.Style = %q, want workspace tutor", out.Advanced.Persona.Style)
	}
}

func TestLoadHierarchical_MissingFilesOK(t *testing.T) {
	out, err := LoadHierarchical(LayerSources{
		SystemFile:   "/nonexistent/system.yaml",
		UserFile:     "/nonexistent/user.yaml",
		EnforcedFile: "/nonexistent/enforced.yaml",
	})
	if err != nil {
		t.Fatalf("missing files should not error: %v", err)
	}
	// Default profile (yolo) applies.
	if out.Profile != "yolo" {
		t.Errorf("Profile = %q, want yolo", out.Profile)
	}
}

func TestLoadHierarchical_ParseError(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.yaml")
	writeYAML(t, bad, "not: [valid: unclosed")

	if _, err := LoadHierarchical(LayerSources{UserFile: bad}); err == nil {
		t.Fatal("expected parse error from malformed YAML")
	}
}

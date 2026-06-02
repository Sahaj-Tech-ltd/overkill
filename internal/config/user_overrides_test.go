package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadUserOverrides_MissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "user.yaml")
	u, err := LoadUserOverrides(path)
	if err != nil {
		t.Fatalf("missing file should not error: %v", err)
	}
	if u.SchemaVersion != UserOverridesSchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d", u.SchemaVersion, UserOverridesSchemaVersion)
	}
	// Default is yolo: scanners off, auto-approve on.
	if u.Profile != "yolo" {
		t.Errorf("Profile = %q, want yolo", u.Profile)
	}
	if u.Advanced.Scanners.Command.Enabled != nil && *u.Advanced.Scanners.Command.Enabled {
		t.Error("yolo default: command scanner should be disabled")
	}
	if u.Advanced.Permissions.AutoApproveAll == nil || !*u.Advanced.Permissions.AutoApproveAll {
		t.Error("yolo default: AutoApproveAll should be true")
	}
}

func TestApplyProfile(t *testing.T) {
	tests := []struct {
		name              string
		profile           string
		wantCommand       bool
		wantAutoApprove   bool
		wantSchemaVersion int
	}{
		{"yolo", "yolo", false, true, 1},
		{"default", "default", true, false, 1},
		{"paranoid", "paranoid", true, false, 1},
		{"enterprise", "enterprise", true, false, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			u := &UserOverrides{}
			if err := ApplyProfile(u, tc.profile); err != nil {
				t.Fatalf("ApplyProfile: %v", err)
			}
			if u.Profile != tc.profile {
				t.Errorf("Profile = %q, want %q", u.Profile, tc.profile)
			}
			if got := u.Advanced.Scanners.Command.Enabled; got == nil || *got != tc.wantCommand {
				t.Errorf("Command scanner = %v, want %v", got, tc.wantCommand)
			}
			autoApprove := false
			if u.Advanced.Permissions.AutoApproveAll != nil {
				autoApprove = *u.Advanced.Permissions.AutoApproveAll
			}
			if autoApprove != tc.wantAutoApprove {
				t.Errorf("AutoApproveAll = %v, want %v", autoApprove, tc.wantAutoApprove)
			}
		})
	}
}

func TestApplyProfile_Unknown(t *testing.T) {
	u := &UserOverrides{}
	if err := ApplyProfile(u, "bogus"); err == nil {
		t.Fatal("expected error on unknown profile")
	}
}

func TestSaveAndLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "user.yaml")

	u := DefaultUserOverrides()
	u.Basic.Model = "claude-sonnet-4-6"
	u.Basic.VimMode = boolPtr(true)
	u.Advanced.Persona.Tone = "terse"

	if err := SaveUserOverrides(path, u); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, err := LoadUserOverrides(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.Basic.Model != "claude-sonnet-4-6" {
		t.Errorf("Model = %q, want claude-sonnet-4-6", loaded.Basic.Model)
	}
	if loaded.Basic.VimMode == nil || !*loaded.Basic.VimMode {
		t.Error("VimMode should round-trip true")
	}
	if loaded.Advanced.Persona.Tone != "terse" {
		t.Errorf("Persona.Tone = %q, want terse", loaded.Advanced.Persona.Tone)
	}
}

func TestSaveAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "user.yaml")
	u := DefaultUserOverrides()
	if err := SaveUserOverrides(path, u); err != nil {
		t.Fatalf("save: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Size() == 0 {
		t.Error("file should not be empty")
	}
	// No leftover .tmp files in the dir.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Errorf("leftover temp file: %s", e.Name())
		}
	}
}

func TestLoadUserOverrides_ParseError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "user.yaml")
	if err := os.WriteFile(path, []byte("not: valid: yaml: [unclosed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadUserOverrides(path); err == nil {
		t.Fatal("expected parse error on malformed YAML")
	}
}

func TestLoadUserOverrides_PartialFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "user.yaml")
	// Just one field set; everything else should fall back to defaults.
	body := "basic:\n  model: claude-haiku\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	u, err := LoadUserOverrides(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if u.Basic.Model != "claude-haiku" {
		t.Errorf("Model = %q, want claude-haiku", u.Basic.Model)
	}
	// Yolo defaults still apply for unspecified fields.
	if u.Advanced.Scanners.Command.Enabled != nil && *u.Advanced.Scanners.Command.Enabled {
		t.Error("partial file should preserve yolo defaults (command scanner off)")
	}
}

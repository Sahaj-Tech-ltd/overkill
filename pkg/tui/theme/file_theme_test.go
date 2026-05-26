package theme

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseFileTheme_OverridesAccentOnly(t *testing.T) {
	src := `extends = "catppuccin"
[colors]
accent = "#ff79c6"
`
	ft, err := ParseFileTheme("dracula-ish", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := string(ft.Accent()); got != "#ff79c6" {
		t.Errorf("accent override lost: %q", got)
	}
	// Untouched slot must come from catppuccin base.
	base := &Catppuccin{}
	if got, want := string(ft.Primary()), string(base.Primary()); got != want {
		t.Errorf("unset slot should inherit from base. got %q want %q", got, want)
	}
}

func TestParseFileTheme_DefaultExtendsIsCatppuccin(t *testing.T) {
	src := `[colors]
accent = "#abcdef"
`
	ft, err := ParseFileTheme("min", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	base := &Catppuccin{}
	if got, want := string(ft.Primary()), string(base.Primary()); got != want {
		t.Errorf("missing extends should default to catppuccin: got %q want %q", got, want)
	}
}

func TestParseFileTheme_UnknownExtendsErrors(t *testing.T) {
	src := `extends = "dracula"
[colors]
accent = "#ff79c6"
`
	_, err := ParseFileTheme("typo", []byte(src))
	if err == nil {
		t.Fatal("expected error for unknown base theme")
	}
	if !strings.Contains(err.Error(), "dracula") {
		t.Errorf("error should mention the bad name: %v", err)
	}
}

func TestParseFileTheme_TildeAccentNoHashAutoAdded(t *testing.T) {
	src := `extends = "catppuccin"
[colors]
accent = "ff79c6"
`
	ft, err := ParseFileTheme("nohash", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := string(ft.Accent()); got != "#ff79c6" {
		t.Errorf("missing # should be auto-added: %q", got)
	}
}

func TestParseFileTheme_TokyoBase(t *testing.T) {
	src := `extends = "tokyo-night"
[colors]
accent = "#fafafa"
`
	ft, err := ParseFileTheme("custom", []byte(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	tokyo := &TokyoNight{}
	if got, want := string(ft.Background()), string(tokyo.Background()); got != want {
		t.Errorf("tokyo background not inherited: got %q want %q", got, want)
	}
}

func TestParseFileTheme_BadTOML(t *testing.T) {
	if _, err := ParseFileTheme("bad", []byte("this = is = not toml")); err == nil {
		t.Error("expected parse error on malformed TOML")
	}
}

func TestLoadFromDir_MissingDirIsOK(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "does-not-exist")
	if err := LoadFromDir(dir); err != nil {
		t.Errorf("missing dir should be silent, got %v", err)
	}
	if got := FileThemes(); len(got) != 0 {
		t.Errorf("missing dir should leave registry empty, got %d", len(got))
	}
}

func TestLoadFromDir_LoadsTOMLFiles(t *testing.T) {
	dir := t.TempDir()
	must := func(p, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, p), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	must("solar.toml", `extends = "catppuccin"
[colors]
accent = "#fbbf24"
`)
	must("vapor.toml", `extends = "tokyo-night"
[colors]
accent = "#a78bfa"
`)
	must("ignored.txt", "not a theme")

	if err := LoadFromDir(dir); err != nil {
		t.Fatalf("load: %v", err)
	}
	got := FileThemes()
	if len(got) != 2 {
		t.Fatalf("want 2 themes, got %d: %v", len(got), keysOf(got))
	}
	if _, ok := got["solar"]; !ok {
		t.Error("solar theme missing")
	}
	if _, ok := got["vapor"]; !ok {
		t.Error("vapor theme missing")
	}
}

func TestLoadFromDir_BuiltinNameRejected(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "catppuccin.toml"), []byte(`[colors]
accent = "#000000"
`), 0o644); err != nil {
		t.Fatal(err)
	}
	err := LoadFromDir(dir)
	if err == nil {
		t.Fatal("expected error when shadowing built-in")
	}
	if !strings.Contains(err.Error(), "built-in") {
		t.Errorf("error should mention built-in conflict: %v", err)
	}
	// Built-in must still be reachable.
	if ByName("catppuccin") == nil {
		t.Error("built-in catppuccin must remain available")
	}
}

func TestLoadFromDir_BadFileDoesntBlockGoodOnes(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "good.toml"), []byte(`[colors]
accent = "#ff0000"
`), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "bad.toml"), []byte("this = is = broken"), 0o644)

	if err := LoadFromDir(dir); err == nil {
		t.Error("expected error for bad.toml")
	}
	if _, ok := FileThemes()["good"]; !ok {
		t.Error("good theme should still load when bad one fails")
	}
}

func TestRegistry_IncludesFileThemes(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "neon.toml"), []byte(`[colors]
accent = "#00ffff"
`), 0o644)
	_ = LoadFromDir(dir)

	r := Registry()
	if _, ok := r["catppuccin"]; !ok {
		t.Error("built-in catppuccin missing from registry")
	}
	if _, ok := r["neon"]; !ok {
		t.Error("loaded theme missing from registry")
	}

	// Cleanup so other tests in this package don't see neon.
	_ = LoadFromDir(filepath.Join(dir, "deleted"))
}

func TestNames_BuiltInsFirstThenAlphabetized(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"zebra.toml", "alpha.toml", "mango.toml"} {
		_ = os.WriteFile(filepath.Join(dir, n), []byte(`[colors]
accent = "#000000"
`), 0o644)
	}
	_ = LoadFromDir(dir)
	got := Names()

	if got[0] != "catppuccin" || got[1] != "tokyo-night" {
		t.Errorf("built-ins not in front: %v", got)
	}
	if got[2] != "alpha" || got[3] != "mango" || got[4] != "zebra" {
		t.Errorf("user themes not alphabetized after built-ins: %v", got)
	}
	_ = LoadFromDir(filepath.Join(dir, "deleted"))
}

func keysOf(m map[string]Theme) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

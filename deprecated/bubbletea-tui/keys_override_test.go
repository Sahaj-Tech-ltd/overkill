package tui

import (
	"os"
	"path/filepath"
	"testing"
)

// resetKeysForTest restores the package-level Keys var to compiled-in
// defaults so test ordering doesn't bleed between cases. Mirrors the
// init in keys.go.
func resetKeysForTest() {
	Keys = defaultKeys()
}

func TestLoadKeyOverrides_MissingFileIsOK(t *testing.T) {
	resetKeysForTest()
	if err := LoadKeyOverrides("/nonexistent/path/keys.toml"); err != nil {
		t.Errorf("missing file should not error, got %v", err)
	}
}

func TestLoadKeyOverrides_EmptyPathIsOK(t *testing.T) {
	resetKeysForTest()
	if err := LoadKeyOverrides(""); err != nil {
		t.Errorf("empty path should not error, got %v", err)
	}
}

func TestLoadKeyOverrides_BadTOMLErrors(t *testing.T) {
	resetKeysForTest()
	dir := t.TempDir()
	path := filepath.Join(dir, "keys.toml")
	if err := os.WriteFile(path, []byte("not valid toml { ["), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := LoadKeyOverrides(path); err == nil {
		t.Error("malformed TOML should return an error so the user can fix it")
	}
}

func TestLoadKeyOverrides_AppliesRemap(t *testing.T) {
	resetKeysForTest()
	dir := t.TempDir()
	path := filepath.Join(dir, "keys.toml")
	content := `
quit = ["ctrl+q"]
help = ["ctrl+h", "f1"]
fork = ["ctrl+y"]
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := LoadKeyOverrides(path); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := Keys.Quit.Keys(); !equalKeySet(got, []string{"ctrl+q"}) {
		t.Errorf("Quit keys = %v, want [ctrl+q]", got)
	}
	if got := Keys.Help.Keys(); !equalKeySet(got, []string{"ctrl+h", "f1"}) {
		t.Errorf("Help keys = %v, want [ctrl+h, f1]", got)
	}
	if got := Keys.Fork.Keys(); !equalKeySet(got, []string{"ctrl+y"}) {
		t.Errorf("Fork keys = %v, want [ctrl+y]", got)
	}
	// Untouched binding stays at default.
	if got := Keys.Commands.Keys(); !equalKeySet(got, []string{"ctrl+k"}) {
		t.Errorf("Commands not overridden but changed; got %v", got)
	}
}

func TestLoadKeyOverrides_EmptySliceDisables(t *testing.T) {
	resetKeysForTest()
	dir := t.TempDir()
	path := filepath.Join(dir, "keys.toml")
	if err := os.WriteFile(path, []byte("status = []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := LoadKeyOverrides(path); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if Keys.Status.Enabled() {
		t.Errorf("explicit empty slice should disable the binding")
	}
}

func equalKeySet(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

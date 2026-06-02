package introspection

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteCodebaseFromScan_GoExports(t *testing.T) {
	src := t.TempDir()
	// Minimal Go module with one exported func + one exported type.
	if err := os.WriteFile(filepath.Join(src, "go.mod"),
		[]byte("module example.test\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	body := `package mypkg

// PublicFunc is exported.
func PublicFunc() string { return "ok" }

func privateFn() {}

type PublicType struct{ X int }

const PublicConst = 42
`
	if err := os.WriteFile(filepath.Join(src, "lib.go"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	out := t.TempDir()
	f, err := WriteCodebaseFromScan(src, out)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if !f.Exists {
		t.Fatal("expected exists")
	}

	got, err := os.ReadFile(f.Path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)

	for _, want := range []string{
		"# Codebase overview",
		"PublicFunc",
		"PublicType",
		"PublicConst",
		"mypkg",
		"example.test", // module name from go.mod
		"go.mod",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("CODEBASE.md missing %q\nbody:\n%s", want, s)
		}
	}
	if strings.Contains(s, "privateFn") {
		t.Error("CODEBASE.md should not include unexported privateFn")
	}
}

func TestLoadCodebaseSnippet_TruncatesAndPrefixes(t *testing.T) {
	dir := t.TempDir()
	body := strings.Repeat("x", 50)
	if err := os.WriteFile(filepath.Join(dir, string(FileCodebase)), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got := LoadCodebaseSnippet(dir, 100)
	if !strings.HasPrefix(got, "## Project context") {
		t.Errorf("expected header prefix, got %q", got[:30])
	}
	if !strings.Contains(got, body) {
		t.Errorf("expected body in snippet")
	}

	// Truncation case.
	long := strings.Repeat("y", 300)
	if err := os.WriteFile(filepath.Join(dir, string(FileCodebase)), []byte(long), 0o644); err != nil {
		t.Fatal(err)
	}
	got = LoadCodebaseSnippet(dir, 100)
	if !strings.Contains(got, "[truncated]") {
		t.Errorf("expected truncation marker, got %q", got)
	}
}

func TestLoadCodebaseSnippet_MissingReturnsEmpty(t *testing.T) {
	if got := LoadCodebaseSnippet(t.TempDir(), 0); got != "" {
		t.Errorf("expected empty for missing file, got %q", got)
	}
}

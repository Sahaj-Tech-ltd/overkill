package introspection

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWritePRP_FreshScaffold(t *testing.T) {
	dir := t.TempDir()
	res, err := WritePRP(PRPInputs{
		ProjectName: "my-app",
		RepoRoot:    "/tmp/my-app",
		Languages:   []string{"Go", "TypeScript"},
		OutputDir:   dir,
	})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if !res.Created {
		t.Fatal("expected Created=true on fresh write")
	}
	body, _ := os.ReadFile(res.Path)
	for _, want := range []string{
		"my-app",
		"## Purpose",
		"## Stack",
		"Go, TypeScript",
		"## Active goals",
		"## Conventions",
		"## Out of scope",
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("missing %q in scaffold", want)
		}
	}
}

func TestWritePRP_PreservesExistingFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "PRP.md"), []byte("hand-curated"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := WritePRP(PRPInputs{ProjectName: "x", OutputDir: dir})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if res.Created {
		t.Fatal("should not have overwritten existing file")
	}
	body, _ := os.ReadFile(res.Path)
	if string(body) != "hand-curated" {
		t.Fatalf("body changed: %s", body)
	}
}

func TestLoadPRPSnippet(t *testing.T) {
	dir := t.TempDir()
	body := strings.Repeat("a", 1000)
	if err := os.WriteFile(filepath.Join(dir, "PRP.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got := LoadPRPSnippet(dir, 500)
	if !strings.Contains(got, "[truncated]") {
		t.Fatalf("expected truncation: len=%d", len(got))
	}
	if got2 := LoadPRPSnippet(dir, 0); got2 != body {
		t.Fatalf("max=0 should return full: len got=%d want=%d", len(got2), len(body))
	}
	if missing := LoadPRPSnippet(t.TempDir(), 100); missing != "" {
		t.Fatalf("missing file should return empty, got %q", missing)
	}
}

func TestDetectLanguages(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"main.go", "util.go", "App.tsx", "config.yaml", "script.py"} {
		_ = os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644)
	}
	langs := DetectLanguages(dir)
	seen := map[string]bool{}
	for _, l := range langs {
		seen[l] = true
	}
	for _, want := range []string{"Go", "TypeScript", "Python"} {
		if !seen[want] {
			t.Errorf("missing %s in %v", want, langs)
		}
	}
}

func TestWritePRP_RequiresOutputDir(t *testing.T) {
	if _, err := WritePRP(PRPInputs{}); err == nil {
		t.Fatal("expected error on missing output_dir")
	}
}

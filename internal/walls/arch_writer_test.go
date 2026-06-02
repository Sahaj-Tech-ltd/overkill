package walls

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// minimalProject creates a tiny scannable directory so EnsureArch has
// something to summarise (an empty dir produces a minimal but valid
// arch file).
func minimalProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/x\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "internal", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "internal", "x", "x.go"), []byte("package x\n\nfunc Hello() string { return \"hi\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestEnsureArch_CreatesWhenAbsent(t *testing.T) {
	dir := minimalProject(t)
	created, err := EnsureArch(dir)
	if err != nil {
		t.Fatalf("EnsureArch: %v", err)
	}
	if !created {
		t.Error("expected created=true on first call")
	}
	data, err := os.ReadFile(filepath.Join(dir, ArchFile))
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	for _, want := range []string{
		"# Architecture", "Wall 2", "Deletion test", "ADRs",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("arch file missing %q section", want)
		}
	}
}

func TestEnsureArch_NoOpWhenPresent(t *testing.T) {
	dir := minimalProject(t)
	created1, _ := EnsureArch(dir)
	created2, err := EnsureArch(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !created1 || created2 {
		t.Errorf("expected created sequence true→false, got %v→%v", created1, created2)
	}
}

func TestEnsureArch_EmptyRootIsNoOp(t *testing.T) {
	created, err := EnsureArch("")
	if err != nil {
		t.Errorf("empty root should not error, got %v", err)
	}
	if created {
		t.Error("empty root should not report created=true")
	}
}

func TestEnsureGlossary_CreatesTemplate(t *testing.T) {
	dir := t.TempDir()
	created, err := EnsureGlossary(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Error("expected glossary creation on first call")
	}
	data, err := os.ReadFile(filepath.Join(dir, GlossaryFile))
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	for _, want := range []string{
		"Glossary", "tracer-bullet-issue", "HITL", "deletion-test",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("glossary template missing %q seed term", want)
		}
	}
}

func TestEnsureGlossary_NoOpWhenPresent(t *testing.T) {
	dir := t.TempDir()
	_, _ = EnsureGlossary(dir)
	created, err := EnsureGlossary(dir)
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Error("second EnsureGlossary should not recreate")
	}
}

func TestRenderArchFromScan_IncludesPackages(t *testing.T) {
	dir := minimalProject(t)
	body, err := renderArchFromScan(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "go.mod") {
		t.Errorf("rendered arch should mention discovered go.mod manifest")
	}
}

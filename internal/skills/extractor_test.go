package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtract_WritesSkillMarkdown(t *testing.T) {
	dir := t.TempDir()
	res, err := Extract(ExtractRequest{
		Name:        "Fix Flaky Tests",
		Description: "Triage and de-flake intermittently failing tests",
		Tags:        []string{"testing", "ci"},
		Triggers:    []string{"flaky test", "test passes locally fails in ci"},
		Transcript:  "user: tests fail randomly\nagent: let's add retry...\n",
		OutputDir:   dir,
	})
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if !res.Created {
		t.Fatal("expected Created=true on first extraction")
	}
	if res.Name != "fix-flaky-tests" {
		t.Fatalf("name sanitization wrong: %q", res.Name)
	}
	body, _ := os.ReadFile(res.Path)
	for _, want := range []string{
		"name: fix-flaky-tests",
		"Fix Flaky Tests",
		"flaky test",
		"## When to use",
		"## Procedure",
		"tests fail randomly",
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("missing %q in output:\n%s", want, body)
		}
	}
}

func TestExtract_PreservesExistingFile(t *testing.T) {
	dir := t.TempDir()
	skillDir := filepath.Join(dir, "thing")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("hand-edited"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Extract(ExtractRequest{Name: "thing", OutputDir: dir, Description: "whatever"})
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if res.Created {
		t.Fatal("should not have overwritten existing file")
	}
	body, _ := os.ReadFile(res.Path)
	if string(body) != "hand-edited" {
		t.Fatalf("file was overwritten: %s", body)
	}
}

func TestExtract_RequiresName(t *testing.T) {
	if _, err := Extract(ExtractRequest{Name: "  ", OutputDir: "/tmp"}); err == nil {
		t.Fatal("expected error on empty name")
	}
}

func TestExtract_RequiresOutputDir(t *testing.T) {
	if _, err := Extract(ExtractRequest{Name: "x"}); err == nil {
		t.Fatal("expected error on missing output_dir")
	}
}

func TestSanitizeName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Hello World", "hello-world"},
		{"!! foo $$ BAR ()", "foo-bar"},
		{"   trim   ", "trim"},
		{strings.Repeat("a", 100), strings.Repeat("a", 60)},
	}
	for _, tc := range cases {
		if got := sanitizeName(tc.in); got != tc.want {
			t.Errorf("sanitizeName(%q)=%q want %q", tc.in, got, tc.want)
		}
	}
}

func TestCondenseTranscript_LongTrimmed(t *testing.T) {
	long := strings.Repeat("xy", 5000)
	got := condenseTranscript(long, 1000)
	if !strings.Contains(got, "[truncated]") {
		t.Fatalf("missing truncation marker: %s", got[:50])
	}
	if len(got) > 1200 {
		t.Fatalf("did not trim: len=%d", len(got))
	}
}

func TestCondenseTranscript_ShortKept(t *testing.T) {
	short := "small body"
	if got := condenseTranscript(short, 1000); got != short {
		t.Fatalf("short input mutated: %q", got)
	}
}

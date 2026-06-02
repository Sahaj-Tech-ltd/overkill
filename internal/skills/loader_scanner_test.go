package skills

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/skills/safety"
)

// stubVerdictScanner returns a configured verdict based on a path
// substring match. Used to simulate VT outcomes deterministically.
type stubVerdictScanner struct {
	hits map[string]safety.Verdict
}

func (s stubVerdictScanner) Name() string { return "stub" }
func (s stubVerdictScanner) Scan(_ context.Context, path string) (safety.Result, error) {
	v := safety.VerdictClean
	for needle, verdict := range s.hits {
		if needle != "" && filepath.Base(path) == needle {
			v = verdict
			break
		}
	}
	return safety.Result{Path: path, Verdict: v, Reason: "stub"}, nil
}

func writeSkill(t *testing.T, root, name, body string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if len(body) < 20 {
		body = body + " -- padded so instructions are at least twenty characters"
	}
	skillMD := "---\n" +
		"name: " + name + "\n" +
		"description: a test skill that has plenty of words to pass the validator gate\n" +
		"---\n\n" + body
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(skillMD), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoader_NoScannerLoadsEverything(t *testing.T) {
	dir := t.TempDir()
	user := filepath.Join(dir, "user")
	writeSkill(t, user, "alpha", "body A")
	writeSkill(t, user, "beta", "body B")

	l := NewLoader("", user)
	skills, err := l.LoadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 2 {
		t.Errorf("expected 2 skills loaded, got %d", len(skills))
	}
	if blocked := l.BlockedSkills(); len(blocked) != 0 {
		t.Errorf("no scanner → no blocks, got %+v", blocked)
	}
}

func TestLoader_ScannerBlocksMaliciousSkill(t *testing.T) {
	dir := t.TempDir()
	user := filepath.Join(dir, "user")
	writeSkill(t, user, "alpha", "innocent")
	writeSkill(t, user, "bad", "malicious payload")

	scanner := stubVerdictScanner{
		hits: map[string]safety.Verdict{
			"SKILL.md": safety.VerdictClean, // default for alpha
		},
	}
	// Override: any file inside the "bad" dir → Malicious.
	scanner = stubVerdictScanner{
		hits: map[string]safety.Verdict{},
	}
	// Use a path-based stub via wrapper.
	flagBad := func(_ context.Context, path string) (safety.Result, error) {
		v := safety.VerdictClean
		if filepath.Base(filepath.Dir(path)) == "bad" {
			v = safety.VerdictMalicious
		}
		return safety.Result{Path: path, Verdict: v}, nil
	}
	_ = scanner
	l := NewLoader("", user).WithScanner(funcScanner{fn: flagBad})
	skills, err := l.LoadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 {
		t.Errorf("malicious skill should be filtered out, got %d skills", len(skills))
	}
	if skills[0].Name != "alpha" {
		t.Errorf("alpha should survive, got %q", skills[0].Name)
	}
	blocked := l.BlockedSkills()
	if len(blocked) != 1 {
		t.Fatalf("expected 1 blocked entry, got %d", len(blocked))
	}
	if blocked[0].Verdict != safety.VerdictMalicious {
		t.Errorf("blocked verdict: %s", blocked[0].Verdict)
	}
}

func TestLoader_ScannerLetsUnknownAndCleanThrough(t *testing.T) {
	dir := t.TempDir()
	user := filepath.Join(dir, "user")
	writeSkill(t, user, "fresh", "brand new")

	// Unknown verdict for everything.
	unk := funcScanner{fn: func(_ context.Context, path string) (safety.Result, error) {
		return safety.Result{Path: path, Verdict: safety.VerdictUnknown, Reason: "not in corpus"}, nil
	}}
	l := NewLoader("", user).WithScanner(unk)
	skills, err := l.LoadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 {
		t.Errorf("Unknown should not block, got %d skills", len(skills))
	}
	if len(l.BlockedSkills()) != 0 {
		t.Errorf("Unknown should not record a block")
	}
}

func TestLoader_BundledSkillsAreNotScanned(t *testing.T) {
	dir := t.TempDir()
	bundled := filepath.Join(dir, "bundled")
	writeSkill(t, bundled, "core", "trusted")

	// Force Malicious — bundled skills must still load.
	mal := funcScanner{fn: func(_ context.Context, path string) (safety.Result, error) {
		return safety.Result{Path: path, Verdict: safety.VerdictMalicious}, nil
	}}
	l := NewLoader(bundled, "").WithScanner(mal)
	skills, err := l.LoadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 {
		t.Errorf("bundled skill should bypass scanner, got %d", len(skills))
	}
	if blocked := l.BlockedSkills(); len(blocked) != 0 {
		t.Errorf("bundled skills should not appear in block list, got %+v", blocked)
	}
}

type funcScanner struct {
	fn func(context.Context, string) (safety.Result, error)
}

func (f funcScanner) Name() string { return "func" }
func (f funcScanner) Scan(ctx context.Context, p string) (safety.Result, error) {
	return f.fn(ctx, p)
}

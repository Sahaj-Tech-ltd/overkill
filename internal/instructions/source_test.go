package instructions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func put(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}
func TestSourceTrustAndBounds(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "project")
	put(t, filepath.Join(root, "AGENTS.md"), "root-marker")
	put(t, filepath.Join(parent, "AGENTS.md"), "untrusted-parent")
	if source, err := New(root, Config{}); err != nil || source != nil {
		t.Fatalf("disabled: %v %v", source, err)
	}
	for _, cfg := range []Config{{Enabled: true, GlobalFile: "relative.md"}, {Enabled: true, Directory: ".."}} {
		if _, err := New(root, cfg); err == nil {
			t.Fatalf("accepted invalid config: %+v", cfg)
		}
	}
	source, err := New(root, Config{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := source.Render(); !strings.Contains(got, "root-marker") || strings.Contains(got, "untrusted-parent") {
		t.Fatal(got)
	}
	outside := filepath.Join(parent, "outside.md")
	put(t, outside, "external-marker")
	os.Remove(filepath.Join(root, "AGENTS.md"))
	if err := os.Symlink(outside, filepath.Join(root, "AGENTS.md")); err != nil {
		t.Fatal(err)
	}
	got := source.Render()
	if strings.Contains(got, "external-marker") || !strings.Contains(got, "outside workspace") {
		t.Fatal(got)
	}
	if err := os.Symlink(parent, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if err := source.SetDirectory("escape"); err == nil {
		t.Fatal("accepted escaping directory symlink")
	}
}

func TestSourceRejectsWholeInvalidFilesAndReportsBudget(t *testing.T) {
	root := t.TempDir()
	put(t, filepath.Join(root, "AGENTS.md"), strings.Repeat("x", MaxFileBytes+1))
	source, err := New(root, Config{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := source.Render(); !strings.Contains(got, "oversized") || strings.Contains(got, strings.Repeat("x", 100)) {
		t.Fatal("oversized file injected")
	}
	put(t, filepath.Join(root, "AGENTS.md"), string([]byte{0xff, 0xfe}))
	if !strings.Contains(source.Render(), "invalid UTF-8") {
		t.Fatal(source.Render())
	}
	global := filepath.Join(t.TempDir(), "global.md")
	put(t, global, strings.Repeat("g", MaxFileBytes))
	put(t, filepath.Join(root, "AGENTS.md"), strings.Repeat("r", MaxFileBytes))
	put(t, filepath.Join(root, "nested", "AGENTS.md"), "nested-omitted")
	source, err = New(root, Config{Enabled: true, GlobalFile: global, Directory: "nested"})
	if err != nil {
		t.Fatal(err)
	}
	got := source.Render()
	if !strings.Contains(got, "total instruction budget exceeded") || strings.Contains(got, "nested-omitted") {
		t.Fatal("total budget not enforced")
	}
	os.Remove(filepath.Join(root, "nested", "AGENTS.md"))
	if err := os.Mkdir(filepath.Join(root, "nested", "AGENTS.md"), 0700); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(source.Render(), "non-regular") {
		t.Fatal("directory accepted as instruction file")
	}
}

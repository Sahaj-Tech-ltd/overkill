package viewer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileViewOpen(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.go")
	os.WriteFile(p, []byte("package x\n\nfunc main() {}\n"), 0o644)
	v := NewFileView(p)
	v.SetSize(80, 10)
	out := v.View()
	if !strings.Contains(out, "x.go") {
		t.Errorf("missing path in view")
	}
	if v.Path() != p {
		t.Errorf("path mismatch")
	}
}

func TestFileViewMissing(t *testing.T) {
	v := NewFileView("/no/such/file.txt")
	v.SetSize(80, 10)
	out := v.View()
	if !strings.Contains(out, "error") {
		t.Errorf("no error in view: %q", out)
	}
}

func TestScrollClamp(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	os.WriteFile(p, []byte(strings.Repeat("a\n", 50)), 0o644)
	v := NewFileView(p)
	v.SetSize(40, 10)
	v.ScrollDown(1000)
	v.View() // should not crash
	if v.scroll < 0 {
		t.Errorf("negative scroll")
	}
	v.ScrollUp(1000)
	if v.scroll != 0 {
		t.Errorf("scroll not clamped to 0")
	}
}

func TestEmptyView(t *testing.T) {
	v := NewFileView("")
	out := v.View()
	if !strings.Contains(out, "no file") {
		t.Errorf("expected no-file hint")
	}
}

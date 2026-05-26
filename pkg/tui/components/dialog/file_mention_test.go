package dialog

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFileMention_Filter(t *testing.T) {
	d := NewFileMentionDialog()
	d.all = []string{"cmd/main.go", "internal/agent/agent.go", "pkg/tui/tui.go", "README.md"}
	d.filter()
	if len(d.filtered) != 4 {
		t.Fatalf("expected 4 files unfiltered, got %d", len(d.filtered))
	}
	d.SetQuery("agent")
	if len(d.filtered) != 1 || d.filtered[0] != "internal/agent/agent.go" {
		t.Fatalf("unexpected filtered: %v", d.filtered)
	}
	d.SetQuery("GO")
	if len(d.filtered) != 3 {
		t.Fatalf("expected 3 case-insensitive matches, got %v", d.filtered)
	}
}

func TestFileMention_LoadFromCwd(t *testing.T) {
	dir := t.TempDir()
	// Two files; use fallback walker (no git init).
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "b.txt"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}

	d := NewFileMentionDialog()
	d.LoadFromCwd(dir, false)
	if !d.IsLoaded() {
		t.Fatal("expected loaded")
	}
	files := d.All()
	if len(files) < 2 {
		t.Fatalf("expected >=2 files, got %d (%v)", len(files), files)
	}
	// Cache: subsequent call without force shouldn't refetch (no easy probe,
	// but we can confirm idempotence).
	d.LoadFromCwd(dir, false)
	if len(d.All()) != len(files) {
		t.Fatalf("expected stable file list across calls")
	}
}

func TestFileMention_Insertion(t *testing.T) {
	d := NewFileMentionDialog()
	d.all = []string{"a.go", "b.go"}
	d.filter()
	d.Show = true
	d.cursor = 1
	if d.filtered[d.cursor] != "b.go" {
		t.Fatalf("expected b.go, got %s", d.filtered[d.cursor])
	}
}

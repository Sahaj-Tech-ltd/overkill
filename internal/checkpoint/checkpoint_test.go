package checkpoint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSnapshot_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	work := t.TempDir()
	a := filepath.Join(work, "a.txt")
	b := filepath.Join(work, "b.txt")
	if err := os.WriteFile(a, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("world"), 0o600); err != nil {
		t.Fatal(err)
	}

	m, _ := NewManager(dir, 5)
	man, err := m.Snapshot("s1", "test", []string{a, b})
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if len(man.Entries) != 2 {
		t.Fatalf("entries=%d want 2", len(man.Entries))
	}

	// Mutate after snapshot.
	_ = os.WriteFile(a, []byte("DELETED"), 0o600)
	_ = os.Remove(b)

	skipped, err := m.Restore(man.ID)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if len(skipped) != 0 {
		t.Fatalf("unexpected skipped: %v", skipped)
	}
	if got, _ := os.ReadFile(a); string(got) != "hello" {
		t.Fatalf("a not restored, got %q", got)
	}
	if got, _ := os.ReadFile(b); string(got) != "world" {
		t.Fatalf("b not restored, got %q", got)
	}
}

func TestSnapshot_NonExistentFileRollbackRemovesPostCreation(t *testing.T) {
	dir := t.TempDir()
	work := t.TempDir()
	target := filepath.Join(work, "new.txt")

	m, _ := NewManager(dir, 5)
	man, err := m.Snapshot("s1", "before-create", []string{target})
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if len(man.Entries) != 1 || man.Entries[0].Existed {
		t.Fatalf("expected Existed=false, got %+v", man.Entries)
	}

	// Simulate the agent creating it.
	_ = os.WriteFile(target, []byte("oops"), 0o600)

	if _, err := m.Restore(man.ID); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("file should have been removed on rollback: err=%v", err)
	}
}

func TestSnapshot_LargeFileSkipped(t *testing.T) {
	dir := t.TempDir()
	work := t.TempDir()
	big := filepath.Join(work, "big.bin")
	if err := os.WriteFile(big, make([]byte, 2*1024*1024), 0o644); err != nil {
		t.Fatal(err)
	}

	m, _ := NewManager(dir, 5)
	man, err := m.Snapshot("s1", "big", []string{big})
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if man.Entries[0].Sha256 != "" {
		t.Fatalf("large file should be skipped (empty sha)")
	}

	skipped, err := m.Restore(man.ID)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	if len(skipped) != 1 {
		t.Fatalf("expected 1 skipped, got %v", skipped)
	}
}

func TestList_NewestFirstAndPrune(t *testing.T) {
	dir := t.TempDir()
	work := t.TempDir()
	target := filepath.Join(work, "x.txt")
	_ = os.WriteFile(target, []byte("v1"), 0o600)

	m, _ := NewManager(dir, 2) // keep only 2 per session
	for i := 0; i < 4; i++ {
		if _, err := m.Snapshot("s1", "iter", []string{target}); err != nil {
			t.Fatal(err)
		}
	}
	list, err := m.List("s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("got %d want 2 (pruned)", len(list))
	}
	if !list[0].CreatedAt.After(list[1].CreatedAt) {
		t.Fatal("not sorted newest-first")
	}
}

func TestList_AllSessionsWhenEmpty(t *testing.T) {
	dir := t.TempDir()
	work := t.TempDir()
	target := filepath.Join(work, "x.txt")
	_ = os.WriteFile(target, []byte("v"), 0o600)

	m, _ := NewManager(dir, 5)
	_, _ = m.Snapshot("s1", "", []string{target})
	_, _ = m.Snapshot("s2", "", []string{target})
	all, _ := m.List("")
	if len(all) != 2 {
		t.Fatalf("got %d want 2", len(all))
	}
}

func TestRestore_UnknownIDFails(t *testing.T) {
	dir := t.TempDir()
	m, _ := NewManager(dir, 5)
	_, err := m.Restore("nope")
	if err == nil {
		t.Fatal("expected error for unknown id")
	}
}

func TestSnapshot_ReasonStored(t *testing.T) {
	dir := t.TempDir()
	work := t.TempDir()
	target := filepath.Join(work, "a")
	_ = os.WriteFile(target, []byte("x"), 0o600)
	m, _ := NewManager(dir, 5)
	man, _ := m.Snapshot("s", "before patch foo.go", []string{target})
	if !strings.Contains(man.Reason, "patch foo.go") {
		t.Fatalf("reason not stored: %q", man.Reason)
	}
}

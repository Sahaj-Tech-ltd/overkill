package session

import (
	"path/filepath"
	"testing"
)

func newTestStash(t *testing.T) *StashStore {
	t.Helper()
	s, err := NewStashStore(filepath.Join(t.TempDir(), "stash.json"))
	if err != nil {
		t.Fatalf("NewStashStore: %v", err)
	}
	return s
}

func TestStash_SaveListGet(t *testing.T) {
	s := newTestStash(t)
	id, err := s.Save("hello world")
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty id")
	}
	entries, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	got, err := s.Get(id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "hello world" {
		t.Fatalf("got %q want hello world", got)
	}
}

func TestStash_SaveEmptyError(t *testing.T) {
	s := newTestStash(t)
	if _, err := s.Save(""); err == nil {
		t.Fatal("expected error on empty save")
	}
}

func TestStash_ListNewestFirst(t *testing.T) {
	s := newTestStash(t)
	_, _ = s.Save("first")
	_, _ = s.Save("second")
	_, _ = s.Save("third")
	entries, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("got %d entries", len(entries))
	}
	if entries[0].Text != "third" {
		t.Fatalf("expected newest first, got %q", entries[0].Text)
	}
}

func TestStash_Delete(t *testing.T) {
	s := newTestStash(t)
	id, _ := s.Save("doomed")
	if err := s.Delete(id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	entries, _ := s.List()
	if len(entries) != 0 {
		t.Fatalf("expected empty after delete, got %d", len(entries))
	}
	// Missing id should not error.
	if err := s.Delete("nonexistent"); err != nil {
		t.Fatalf("Delete nonexistent should be no-op, got %v", err)
	}
}

func TestStash_Persist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stash.json")
	s1, _ := NewStashStore(path)
	id, _ := s1.Save("persisted")

	s2, _ := NewStashStore(path)
	got, err := s2.Get(id)
	if err != nil {
		t.Fatalf("Get from reopened store: %v", err)
	}
	if got != "persisted" {
		t.Fatalf("got %q want persisted", got)
	}
}

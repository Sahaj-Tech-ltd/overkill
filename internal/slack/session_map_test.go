package slack

import (
	"path/filepath"
	"testing"
)

func TestSessionMap_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.json")

	m, err := NewSessionMap(path)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if _, ok := m.Get("C1", "1.0"); ok {
		t.Fatal("expected miss")
	}

	id, created, err := m.GetOrCreate("C1", "1.0", "sess-A")
	if err != nil || !created || id != "sess-A" {
		t.Fatalf("first create: id=%q created=%v err=%v", id, created, err)
	}
	id2, created2, err := m.GetOrCreate("C1", "1.0", "sess-B")
	if err != nil || created2 || id2 != "sess-A" {
		t.Fatalf("dedupe: id=%q created=%v err=%v", id2, created2, err)
	}

	// Round-trip: open a fresh map from the same file.
	m2, err := NewSessionMap(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	got, ok := m2.Get("C1", "1.0")
	if !ok || got != "sess-A" {
		t.Fatalf("after reload: got=%q ok=%v", got, ok)
	}

	// Different thread → distinct session.
	other, created3, err := m2.GetOrCreate("C1", "2.0", "sess-C")
	if err != nil || !created3 || other != "sess-C" {
		t.Fatalf("new thread: id=%q created=%v err=%v", other, created3, err)
	}
}

func TestSessionMap_NoPersistence(t *testing.T) {
	m, err := NewSessionMap("")
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if _, _, err := m.GetOrCreate("C", "T", "S"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if got, ok := m.Get("C", "T"); !ok || got != "S" {
		t.Fatalf("got=%q ok=%v", got, ok)
	}
}

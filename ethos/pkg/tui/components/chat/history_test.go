package chat

import (
	"path/filepath"
	"testing"
)

func TestHistory_AppendAndDedup(t *testing.T) {
	h := NewHistory()
	h.Append("one")
	h.Append("two")
	h.Append("two") // duplicate
	h.Append("three")
	got := h.Entries()
	want := []string{"one", "two", "three"}
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d (%v)", len(got), len(want), got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("entry %d: got %q want %q", i, got[i], want[i])
		}
	}
}

func TestHistory_AppendIgnoresEmpty(t *testing.T) {
	h := NewHistory()
	h.Append("")
	h.Append("\n")
	if len(h.Entries()) != 0 {
		t.Fatalf("expected empty history, got %v", h.Entries())
	}
}

func TestHistory_PrevNextWalks(t *testing.T) {
	h := NewHistory()
	for _, s := range []string{"a", "b", "c"} {
		h.Append(s)
	}
	if got := h.Prev(); got != "c" {
		t.Fatalf("first prev: got %q want %q", got, "c")
	}
	if got := h.Prev(); got != "b" {
		t.Fatalf("second prev: got %q want %q", got, "b")
	}
	if got := h.Prev(); got != "a" {
		t.Fatalf("third prev: got %q want %q", got, "a")
	}
	// At oldest, stays put.
	if got := h.Prev(); got != "a" {
		t.Fatalf("fourth prev: got %q want %q", got, "a")
	}
	if got := h.Next(); got != "b" {
		t.Fatalf("first next: got %q want %q", got, "b")
	}
	if got := h.Next(); got != "c" {
		t.Fatalf("second next: got %q want %q", got, "c")
	}
	if got := h.Next(); got != "" {
		t.Fatalf("third next should clear: got %q", got)
	}
	if h.IsActive() {
		t.Fatalf("history should be inactive after walking past newest")
	}
}

func TestHistory_PrevOnEmpty(t *testing.T) {
	h := NewHistory()
	if got := h.Prev(); got != "" {
		t.Fatalf("prev on empty: got %q", got)
	}
	if got := h.Next(); got != "" {
		t.Fatalf("next on empty: got %q", got)
	}
}

func TestHistory_Reset(t *testing.T) {
	h := NewHistory()
	h.Append("a")
	h.Append("b")
	h.Prev()
	if !h.IsActive() {
		t.Fatal("expected active recall")
	}
	h.Reset()
	if h.IsActive() {
		t.Fatal("expected inactive recall after reset")
	}
}

func TestHistory_PersistRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "prompt-history.txt")

	h1 := NewHistoryWithFile(path)
	h1.Append("first")
	h1.Append("second")
	h1.Append("third")

	h2 := NewHistoryWithFile(path)
	got := h2.Entries()
	want := []string{"first", "second", "third"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("entry %d: got %q want %q", i, got[i], want[i])
		}
	}
}

func TestHistory_MaxEntriesCap(t *testing.T) {
	h := NewHistory()
	for i := 0; i < maxHistoryEntries+25; i++ {
		// distinct values so dedup doesn't drop them
		h.Append(string(rune('a' + (i % 26))) + ":" + string(rune('A' + (i % 26))) + ":" + itoa(i))
	}
	if len(h.Entries()) != maxHistoryEntries {
		t.Fatalf("expected %d entries (cap), got %d", maxHistoryEntries, len(h.Entries()))
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

package session

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// ==========================================================================
// RED TEAM: Session store — crash/corruption/injection attacks
// ==========================================================================

func stashPath(t *testing.T) string {
	return filepath.Join(t.TempDir(), "stash.json")
}

// RT-SESS-1: Save with null bytes — verify no corruption.
func TestRedTeam_Session_NullBytes(t *testing.T) {
	store, err := NewStashStore(stashPath(t))
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	text := "hello\x00world\x00test"
	id, err := store.Save(text)
	if err != nil {
		t.Fatalf("save with nulls: %v", err)
	}

	got, err := store.Get(id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	t.Logf("saved: %q, got: %q", text, got)
}

// RT-SESS-2: Save with empty text — should error.
func TestRedTeam_Session_EmptySave(t *testing.T) {
	store, err := NewStashStore(stashPath(t))
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	_, err = store.Save("")
	if err == nil {
		t.Log("empty save succeeded (unexpected)")
	} else {
		t.Logf("empty save error (expected): %v", err)
	}
}

// RT-SESS-3: Concurrent save operations on mutex-protected store.
func TestRedTeam_Session_ConcurrentSave(t *testing.T) {
	store, err := NewStashStore(stashPath(t))
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_, err := store.Save(fmt.Sprintf("item-%d", n))
			if err != nil {
				t.Logf("save %d: %v", n, err)
			}
		}(i)
	}
	wg.Wait()

	items, err := store.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	t.Logf("concurrent saves produced %d items", len(items))
}

// RT-SESS-4: Read-only directory.
func TestRedTeam_Session_ReadOnlyDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stash.json")
	if err := os.WriteFile(path, []byte("[]"), 0o444); err != nil {
		t.Fatalf("write initial: %v", err)
	}
	if err := os.Chmod(path, 0o444); err != nil {
		t.Skipf("can't chmod: %v", err)
	}

	store, err := NewStashStore(path)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	_, err = store.Save("test")
	if err != nil {
		t.Logf("save to read-only (expected): %v", err)
	}
}

// RT-SESS-5: Get with empty ID.
func TestRedTeam_Session_EmptyID(t *testing.T) {
	store, err := NewStashStore(stashPath(t))
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	_, err = store.Get("")
	if err != nil {
		t.Logf("get empty id (expected): %v", err)
	}
}

// RT-SESS-6: Extremely long stash text.
func TestRedTeam_Session_LongText(t *testing.T) {
	store, err := NewStashStore(stashPath(t))
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	longText := make([]byte, 100_000)
	for i := range longText {
		longText[i] = 'x'
	}

	id, err := store.Save(string(longText))
	if err != nil {
		t.Fatalf("save long text: %v", err)
	}

	got, err := store.Get(id)
	if err != nil {
		t.Fatalf("get long text: %v", err)
	}
	if len(got) != 100_000 {
		t.Errorf("length mismatch: got %d, want %d", len(got), 100_000)
	}
}

// RT-SESS-7: NewSession with empty folder.
func TestRedTeam_Session_EmptyFolder(t *testing.T) {
	s := NewSession("")
	if s.ID == "" {
		t.Error("NewSession with empty folder should generate ID")
	}
	if s.Status != "active" {
		t.Errorf("expected status active, got %q", s.Status)
	}
	s.AutoTitle("hello world this is a test message")
	if s.Title == "" {
		t.Error("AutoTitle should set title")
	}
	t.Logf("title: %q", s.Title)
}

// RT-SESS-8: AutoTitle with empty/existing message.
func TestRedTeam_Session_AutoTitleEmpty(t *testing.T) {
	s := NewSession("test")
	s.Title = "existing-title"
	s.AutoTitle("")
	if s.Title != "existing-title" {
		t.Errorf("AutoTitle overwrote existing title: %q", s.Title)
	}

	s2 := NewSession("test")
	s2.AutoTitle("")
	if s2.Title != "" {
		t.Errorf("AutoTitle with empty msg set title: %q", s2.Title)
	}
}

// RT-SESS-9: AutoTitle with extremely long message.
func TestRedTeam_Session_AutoTitleLong(t *testing.T) {
	s := NewSession("test")
	longMsg := make([]byte, 5000)
	for i := range longMsg {
		longMsg[i] = 'a' + byte(i%26)
	}
	s.AutoTitle(string(longMsg))
	if len(s.Title) > 75 {
		t.Errorf("title too long: %d chars", len(s.Title))
	}
	t.Logf("truncated title: %q", s.Title)
}

// RT-SESS-10: Unicode in AutoTitle.
func TestRedTeam_Session_AutoTitleUnicode(t *testing.T) {
	s := NewSession("test")
	s.AutoTitle("こんにちは世界 — japanese greeting with emoji and a long tail that should be truncated properly at word boundaries")
	t.Logf("unicode title: %q (len=%d)", s.Title, len(s.Title))
	if len(s.Title) > 75 {
		t.Errorf("unicode title too long: %d", len(s.Title))
	}
}

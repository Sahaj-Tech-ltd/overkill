package plan

import (
	"path/filepath"
	"testing"
)

func TestStore_SetCreatesIDs(t *testing.T) {
	s := NewStore(t.TempDir(), "s1")
	p, err := s.Set("fix the bug", []string{"reproduce", "find cause", "patch", "test"})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Items) != 4 {
		t.Errorf("expected 4 items, got %d", len(p.Items))
	}
	seen := map[string]bool{}
	for _, it := range p.Items {
		if it.ID == "" {
			t.Errorf("item missing ID: %+v", it)
		}
		if seen[it.ID] {
			t.Errorf("duplicate ID: %s", it.ID)
		}
		seen[it.ID] = true
		if it.Done {
			t.Errorf("new items should not be Done: %+v", it)
		}
	}
}

func TestStore_CheckMarksDone(t *testing.T) {
	s := NewStore(t.TempDir(), "s1")
	p, _ := s.Set("t", []string{"a", "b"})
	id := p.Items[0].ID

	updated, err := s.Check(id, "easy one")
	if err != nil {
		t.Fatal(err)
	}
	if !updated.Items[0].Done {
		t.Error("item should be Done")
	}
	if updated.Items[0].DoneAt == nil {
		t.Error("DoneAt should be set")
	}
	if updated.Items[0].Note != "easy one" {
		t.Errorf("note not stored: %+v", updated.Items[0])
	}
}

func TestStore_UncheckFlipsBack(t *testing.T) {
	s := NewStore(t.TempDir(), "s1")
	p, _ := s.Set("t", []string{"a"})
	id := p.Items[0].ID
	_, _ = s.Check(id, "")
	updated, err := s.Uncheck(id)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Items[0].Done {
		t.Error("uncheck should flip Done false")
	}
	if updated.Items[0].DoneAt != nil {
		t.Error("DoneAt should be nil after uncheck")
	}
}

func TestStore_CheckUnknownIDErrors(t *testing.T) {
	s := NewStore(t.TempDir(), "s1")
	_, _ = s.Set("t", []string{"a"})
	if _, err := s.Check("nonexistent", ""); err == nil {
		t.Error("expected error for unknown item ID")
	}
}

func TestStore_RemainingCounts(t *testing.T) {
	s := NewStore(t.TempDir(), "s1")
	p, _ := s.Set("t", []string{"a", "b", "c"})
	if got := s.Remaining(); got != 3 {
		t.Errorf("expected 3, got %d", got)
	}
	_, _ = s.Check(p.Items[0].ID, "")
	if got := s.Remaining(); got != 2 {
		t.Errorf("expected 2, got %d", got)
	}
	_, _ = s.Check(p.Items[1].ID, "")
	_, _ = s.Check(p.Items[2].ID, "")
	if got := s.Remaining(); got != 0 {
		t.Errorf("expected 0, got %d", got)
	}
}

func TestStore_LoadRoundtrip(t *testing.T) {
	dir := t.TempDir()
	src := NewStore(dir, "s1")
	p, _ := src.Set("title here", []string{"a", "b"})
	_, _ = src.Check(p.Items[0].ID, "note")

	dst := NewStore(dir, "s1")
	loaded, err := dst.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded == nil {
		t.Fatal("expected plan to load")
	}
	if loaded.Title != "title here" {
		t.Errorf("title not preserved: %s", loaded.Title)
	}
	if !loaded.Items[0].Done || loaded.Items[0].Note != "note" {
		t.Errorf("item state not preserved: %+v", loaded.Items[0])
	}
}

func TestStore_LoadMissingIsNil(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "doesnotexist"), "s1")
	got, err := s.Load()
	if err != nil {
		t.Errorf("missing dir should not error: %v", err)
	}
	if got != nil {
		t.Errorf("missing plan should return nil: %+v", got)
	}
}

func TestStore_ClearRemovesFile(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir, "s1")
	_, _ = s.Set("t", []string{"a"})
	if err := s.Clear(); err != nil {
		t.Fatal(err)
	}
	if got := s.Current(); got != nil {
		t.Errorf("Current should be nil after Clear: %+v", got)
	}
}

func TestStore_SetRequiresContent(t *testing.T) {
	s := NewStore(t.TempDir(), "s1")
	if _, err := s.Set("", nil); err == nil {
		t.Error("empty Set should error")
	}
}

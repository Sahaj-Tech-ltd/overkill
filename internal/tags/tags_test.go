package tags

import (
	"path/filepath"
	"testing"
)

func newTestMgr(t *testing.T) *Manager {
	t.Helper()
	dir := t.TempDir()
	m, err := NewManager(filepath.Join(dir, "tags.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestTagAndList(t *testing.T) {
	m := newTestMgr(t)
	if err := m.Tag("a.go", "important", ""); err != nil {
		t.Fatal(err)
	}
	if err := m.Tag("b.go", "important", "needs review"); err != nil {
		t.Fatal(err)
	}
	if err := m.Tag("a.go", "todo", ""); err != nil {
		t.Fatal(err)
	}
	all := m.List()
	if len(all) != 3 {
		t.Fatalf("want 3 tags, got %d", len(all))
	}
	if all[0].Tag != "important" {
		t.Errorf("sorted first should be important, got %q", all[0].Tag)
	}
}

func TestByPathAndByTag(t *testing.T) {
	m := newTestMgr(t)
	m.Tag("x", "a", "")
	m.Tag("x", "b", "")
	m.Tag("y", "a", "")
	if len(m.ByPath("x")) != 2 {
		t.Errorf("byPath x")
	}
	if len(m.ByTag("a")) != 2 {
		t.Errorf("byTag a")
	}
	tags := m.Tags()
	if len(tags) != 2 || tags[0] != "a" || tags[1] != "b" {
		t.Errorf("unique tags wrong: %v", tags)
	}
}

func TestUntag(t *testing.T) {
	m := newTestMgr(t)
	m.Tag("p", "x", "")
	m.Tag("p", "y", "")
	if err := m.Untag("p", "x"); err != nil {
		t.Fatal(err)
	}
	if got := m.ByPath("p"); len(got) != 1 || got[0].Tag != "y" {
		t.Errorf("after untag x: %+v", got)
	}
	if err := m.Untag("p", ""); err != nil {
		t.Fatal(err)
	}
	if got := m.ByPath("p"); len(got) != 0 {
		t.Errorf("after untag *: %+v", got)
	}
}

func TestPersistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tags.jsonl")
	m1, err := NewManager(path)
	if err != nil {
		t.Fatal(err)
	}
	m1.Tag("foo", "bar", "baz")

	m2, err := NewManager(path)
	if err != nil {
		t.Fatal(err)
	}
	got := m2.List()
	if len(got) != 1 || got[0].Note != "baz" {
		t.Errorf("reload: %+v", got)
	}
}

func TestUpdateExistingTagNote(t *testing.T) {
	m := newTestMgr(t)
	m.Tag("p", "t", "first")
	m.Tag("p", "t", "second")
	got := m.ByPath("p")
	if len(got) != 1 || got[0].Note != "second" {
		t.Errorf("expected note=second once, got %+v", got)
	}
}

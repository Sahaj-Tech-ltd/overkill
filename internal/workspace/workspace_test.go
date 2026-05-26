package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func newTestMgr(t *testing.T) *Manager {
	t.Helper()
	dir := t.TempDir()
	m, err := NewManager(filepath.Join(dir, "ws.json"))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestAddAndList(t *testing.T) {
	m := newTestMgr(t)
	a := t.TempDir()
	b := t.TempDir()
	wa, err := m.Add(a, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if wa.Name != "alpha" {
		t.Errorf("name=%q", wa.Name)
	}
	_, _ = m.Add(b, "beta")
	if got := m.List(); len(got) != 2 {
		t.Errorf("want 2 workspaces, got %d", len(got))
	}
}

func TestAddDeduplicatesByPath(t *testing.T) {
	m := newTestMgr(t)
	d := t.TempDir()
	w1, _ := m.Add(d, "first")
	w2, _ := m.Add(d, "second")
	if w1.ID != w2.ID {
		t.Errorf("expected same id for same path")
	}
	if got := m.List(); len(got) != 1 {
		t.Errorf("expected one workspace")
	}
}

func TestSwitchAndCurrent(t *testing.T) {
	m := newTestMgr(t)
	startDir, _ := os.Getwd()
	defer os.Chdir(startDir)

	a := t.TempDir()
	wa, _ := m.Add(a, "alpha")
	got, err := m.Switch(wa.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != wa.ID {
		t.Errorf("switch id mismatch")
	}
	cur := m.Current()
	if cur == nil || cur.ID != wa.ID {
		t.Errorf("current=%v want %v", cur, wa)
	}
	cwd, _ := os.Getwd()
	wantAbs, _ := filepath.EvalSymlinks(a)
	gotAbs, _ := filepath.EvalSymlinks(cwd)
	if wantAbs != gotAbs {
		t.Errorf("cwd=%q want %q", gotAbs, wantAbs)
	}
}

func TestSwitchUnknown(t *testing.T) {
	m := newTestMgr(t)
	if _, err := m.Switch("nope"); err == nil {
		t.Fatal("expected error")
	}
}

func TestPersistence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ws.json")
	m1, _ := NewManager(path)
	m1.Add(t.TempDir(), "p")

	m2, _ := NewManager(path)
	if len(m2.List()) != 1 {
		t.Errorf("reload lost data")
	}
}

func TestRemove(t *testing.T) {
	m := newTestMgr(t)
	w, _ := m.Add(t.TempDir(), "x")
	if err := m.Remove(w.ID); err != nil {
		t.Fatal(err)
	}
	if got := m.List(); len(got) != 0 {
		t.Errorf("not removed: %+v", got)
	}
}

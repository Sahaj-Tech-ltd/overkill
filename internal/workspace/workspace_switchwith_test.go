package workspace

import (
	"errors"
	"os"
	"testing"
)

func TestSwitchWith_FiresCallbackAfterChdir(t *testing.T) {
	m := newTestMgr(t)
	start, _ := os.Getwd()
	defer os.Chdir(start)

	a := t.TempDir()
	w, _ := m.Add(a, "alpha")

	called := false
	gotPath := ""
	_, err := m.SwitchWith(w.ID, func(ws Workspace) error {
		called = true
		gotPath = ws.Path
		return nil
	})
	if err != nil {
		t.Fatalf("SwitchWith: %v", err)
	}
	if !called {
		t.Fatal("expected callback to fire")
	}
	if gotPath == "" {
		t.Fatal("expected callback to receive workspace path")
	}
}

func TestSwitchWith_PropagatesCallbackError(t *testing.T) {
	m := newTestMgr(t)
	start, _ := os.Getwd()
	defer os.Chdir(start)

	a := t.TempDir()
	w, _ := m.Add(a, "alpha")

	stub := errors.New("boom")
	_, err := m.SwitchWith(w.ID, func(Workspace) error { return stub })
	if err == nil {
		t.Fatal("expected error from failing callback")
	}
}

func TestSwitchWith_NilCallbackEqualsSwitch(t *testing.T) {
	m := newTestMgr(t)
	start, _ := os.Getwd()
	defer os.Chdir(start)

	a := t.TempDir()
	w, _ := m.Add(a, "alpha")
	got, err := m.SwitchWith(w.ID, nil)
	if err != nil {
		t.Fatalf("SwitchWith(nil): %v", err)
	}
	if got == nil || got.ID != w.ID {
		t.Fatalf("expected returned workspace, got %v", got)
	}
}

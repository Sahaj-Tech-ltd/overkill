package main

import (
	"context"
	"encoding/json"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"os"
	"path/filepath"
	"testing"
)

func TestSnapshotDirectoryUsesActiveProfile(t *testing.T) {
	home := t.TempDir()
	profile := filepath.Join(t.TempDir(), "isolated profile")
	t.Setenv("HOME", home)
	t.Setenv("OVERKILL_HOME", profile)
	got, err := SnapshotDir()
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(profile, "snapshots") {
		t.Fatalf("snapshot directory = %q; want active profile %q", got, profile)
	}
}

type snapshotTestStore struct {
	session.Store
	sessions []*session.Session
}

func (s snapshotTestStore) List(context.Context, session.ListOptions) ([]*session.Session, error) {
	return s.sessions, nil
}

func TestSnapshotExportsStayInProfileAndDoNotOverwrite(t *testing.T) {
	home := t.TempDir()
	profile := filepath.Join(t.TempDir(), "profile")
	t.Setenv("HOME", home)
	t.Setenv("OVERKILL_HOME", profile)
	s := &session.Session{ID: "session-one", Title: "private session"}
	first, err := writeSessionSnapshot(s)
	if err != nil {
		t.Fatal(err)
	}
	second, err := writeSessionSnapshot(s)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("overwrote earlier snapshot")
	}
	dailySnapshotTick(snapshotTestStore{sessions: []*session.Session{s}})
	entries, err := os.ReadDir(filepath.Join(profile, "snapshots"))
	if err != nil || len(entries) != 3 {
		t.Fatalf("exports: %d, %v", len(entries), err)
	}
	for _, e := range entries {
		path := filepath.Join(profile, "snapshots", e.Name())
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0600 {
			t.Fatalf("snapshot permissions %v", info.Mode())
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var got session.Session
		if err := json.Unmarshal(data, &got); err != nil || got.ID != s.ID || got.Title != s.Title {
			t.Fatalf("snapshot payload %s, %v", data, err)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".overkill")); !os.IsNotExist(err) {
		t.Fatalf("wrote default profile: %v", err)
	}
}

func TestSnapshotRejectsInvalidProfileAndUnsafeIDs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	blocked := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocked, []byte("existing"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OVERKILL_HOME", blocked)
	if _, err := writeSessionSnapshot(&session.Session{ID: "safe"}); err == nil {
		t.Fatal("accepted invalid profile")
	}
	dailySnapshotTick(snapshotTestStore{sessions: []*session.Session{{ID: "safe"}}})
	if _, err := os.Stat(filepath.Join(home, ".overkill")); !os.IsNotExist(err) {
		t.Fatalf("fell back to default profile: %v", err)
	}
	profile := t.TempDir()
	t.Setenv("OVERKILL_HOME", profile)
	for _, id := range []string{"", ".", "..", "../escaped", "a/b", "a\\b", "a\x00b"} {
		if _, err := writeSessionSnapshot(&session.Session{ID: id}); err == nil {
			t.Fatalf("accepted ID %q", id)
		}
	}
	entries, err := os.ReadDir(profile)
	if err != nil || len(entries) != 0 {
		t.Fatalf("unsafe ID wrote files: %v %v", entries, err)
	}
}

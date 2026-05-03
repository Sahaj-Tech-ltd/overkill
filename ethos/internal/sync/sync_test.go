package sync

import (
	"context"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/ethos/internal/config"
	"github.com/Sahaj-Tech-ltd/ethos/internal/session"
)

func newTestStore(t *testing.T) *session.BadgerStore {
	t.Helper()
	dir := t.TempDir()
	st, err := session.NewBadgerStore(dir)
	if err != nil {
		t.Fatalf("badger store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestFileBackendRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	be, err := NewFileBackend(config.SyncFileConfig{Path: dir})
	if err != nil {
		t.Fatalf("file backend: %v", err)
	}
	ctx := context.Background()

	data := []byte("hello world")
	meta := SessionMeta{ID: "abc", Title: "t", UpdatedAt: time.Now().UTC()}
	if err := be.Push(ctx, "abc", data, meta); err != nil {
		t.Fatalf("push: %v", err)
	}
	got, gotMeta, err := be.Pull(ctx, "abc")
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	if string(got) != string(data) {
		t.Fatalf("data mismatch")
	}
	if gotMeta.Title != "t" {
		t.Fatalf("meta title mismatch: %q", gotMeta.Title)
	}

	metas, err := be.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(metas) != 1 {
		t.Fatalf("expected 1 meta got %d", len(metas))
	}

	if err := be.Delete(ctx, "abc"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, _, err := be.Pull(ctx, "abc"); err != ErrNotFound {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestEncodeDecodeSession(t *testing.T) {
	t.Parallel()
	s := session.NewSession("/tmp/x")
	s.Title = "test"
	data, err := EncodeSession(s)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	out, err := DecodeSession(data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Title != "test" || out.ID != s.ID {
		t.Fatalf("roundtrip mismatch")
	}
}

func TestManagerPushPull(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	be, err := NewFileBackend(config.SyncFileConfig{Path: dir})
	if err != nil {
		t.Fatalf("be: %v", err)
	}

	storeA := newTestStore(t)
	storeB := newTestStore(t)
	ctx := context.Background()

	s := session.NewSession("/tmp/x")
	s.Title = "from A"
	if err := storeA.Create(ctx, s); err != nil {
		t.Fatalf("create: %v", err)
	}
	mgrA := NewManager(storeA, be)
	if err := mgrA.PushOne(ctx, s.ID); err != nil {
		t.Fatalf("push: %v", err)
	}

	mgrB := NewManager(storeB, be)
	n, err := mgrB.PullAll(ctx)
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 pulled got %d", n)
	}
	got, err := storeB.Load(ctx, s.ID)
	if err != nil {
		t.Fatalf("load on B: %v", err)
	}
	if got.Title != "from A" {
		t.Fatalf("title not synced: %q", got.Title)
	}
}

func TestResolveLastWriteWins(t *testing.T) {
	t.Parallel()
	mgr := NewManager(nil, nil)
	older := &session.Session{ID: "x", Title: "old", UpdatedAt: time.Unix(1, 0).UTC()}
	newer := &session.Session{ID: "x", Title: "new", UpdatedAt: time.Unix(100, 0).UTC()}
	w, l := mgr.Resolve(older, newer)
	if w.Title != "new" {
		t.Fatalf("expected newer to win, got %q", w.Title)
	}
	if l == nil || l.Title != "[conflict] old" {
		t.Fatalf("expected conflict copy, got %+v", l)
	}
	w, l = mgr.Resolve(newer, older)
	if w.Title != "new" {
		t.Fatalf("expected newer to win again, got %q", w.Title)
	}
	if l == nil || l.Title != "[conflict] old" {
		t.Fatalf("expected conflict copy, got %+v", l)
	}
}

func TestNewBackendDispatch(t *testing.T) {
	t.Parallel()
	if be, err := NewBackend(config.SyncConfig{}); err != nil || be != nil {
		t.Fatalf("disabled should be nil/nil, got %v %v", be, err)
	}
	if _, err := NewBackend(config.SyncConfig{Backend: "bogus"}); err == nil {
		t.Fatalf("expected error for unknown backend")
	}
	be, err := NewBackend(config.SyncConfig{Backend: "file", File: config.SyncFileConfig{Path: t.TempDir()}})
	if err != nil || be == nil || be.Name() != "file" {
		t.Fatalf("file dispatch failed: %v %v", be, err)
	}
}

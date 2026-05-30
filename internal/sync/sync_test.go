package sync

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("postgres: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func newTestStore(t *testing.T) *session.PostgresStore {
	t.Helper()
	st := session.NewPostgresStore(openTestDB(t))
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// ---------------------------------------------------------------------------
// FileBackend
// ---------------------------------------------------------------------------

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
	if err != nil || string(got) != string(data) || gotMeta.Title != "t" {
		t.Fatalf("pull: data=%q meta=%q err=%v", got, gotMeta.Title, err)
	}
	metas, _ := be.List(ctx)
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

func TestFileBackendErrors(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	be, _ := NewFileBackend(config.SyncFileConfig{Path: dir})
	ctx := context.Background()

	// Empty path
	if _, err := NewFileBackend(config.SyncFileConfig{Path: ""}); err == nil {
		t.Fatal("expected error for empty path")
	}
	// Mkdir error
	blocker := filepath.Join(dir, "blocker")
	os.WriteFile(blocker, []byte("x"), 0o644)
	if _, err := NewFileBackend(config.SyncFileConfig{Path: blocker + "/sub"}); err == nil {
		t.Fatal("expected mkdir error")
	}
	// Pull not found
	if _, _, err := be.Pull(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	// Delete not found
	if err := be.Delete(ctx, "nope"); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	// Delete partial (blob only)
	os.WriteFile(filepath.Join(dir, "orphan.json.gz"), []byte("data"), 0o644)
	if err := be.Delete(ctx, "orphan"); err != nil {
		t.Fatalf("partial delete should succeed: %v", err)
	}
	// List nonexistent dir
	bad := &FileBackend{root: "/nonexistent/xyz"}
	if _, err := bad.List(ctx); err == nil {
		t.Fatal("expected error for nonexistent dir")
	}
}

func TestFileBackendPullMissingMeta(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	be, _ := NewFileBackend(config.SyncFileConfig{Path: dir})
	ctx := context.Background()
	data := []byte("blob")
	meta := SessionMeta{ID: "t1", Title: "test", UpdatedAt: time.Now().UTC()}
	be.Push(ctx, "t1", data, meta)
	os.Remove(filepath.Join(dir, "t1.meta.json"))
	got, gotMeta, err := be.Pull(ctx, "t1")
	if err != nil || string(got) != string(data) || gotMeta.ID != "t1" {
		t.Fatalf("pull without meta: data=%q id=%q err=%v", got, gotMeta.ID, err)
	}
}

func TestFileBackendListView(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	be, _ := NewFileBackend(config.SyncFileConfig{Path: dir})
	ctx := context.Background()
	// Empty list
	if metas, _ := be.List(ctx); len(metas) != 0 {
		t.Fatal("expected empty")
	}
	// Non-meta files ignored
	be.Push(ctx, "s1", []byte("x"), SessionMeta{ID: "s1", Title: "h", UpdatedAt: time.Now().UTC()})
	os.WriteFile(filepath.Join(dir, "junk.txt"), []byte("j"), 0o644)
	os.WriteFile(filepath.Join(dir, "bad.meta.json"), []byte("not json"), 0o644)
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	metas, _ := be.List(ctx)
	if len(metas) != 1 || metas[0].Title != "h" {
		t.Fatalf("expected 1 meta 'h', got %d", len(metas))
	}
}

func TestFileBackendPushOverwrite(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	be, _ := NewFileBackend(config.SyncFileConfig{Path: dir})
	ctx := context.Background()
	be.Push(ctx, "dup", []byte("a"), SessionMeta{ID: "dup", Title: "first", UpdatedAt: time.Now().UTC()})
	be.Push(ctx, "dup", []byte("bb"), SessionMeta{ID: "dup", Title: "second", UpdatedAt: time.Now().UTC()})
	got, gotMeta, _ := be.Pull(ctx, "dup")
	if string(got) != "bb" || gotMeta.Title != "second" {
		t.Fatalf("overwrite: %q / %q", got, gotMeta.Title)
	}
}

// ---------------------------------------------------------------------------
// GitBackend
// ---------------------------------------------------------------------------

func TestGitBackendRoundTrip(t *testing.T) {
	dir := t.TempDir()
	be, err := NewGitBackend(config.SyncGitConfig{LocalDir: dir, Branch: "main"})
	if err != nil {
		t.Fatalf("new git backend: %v", err)
	}
	if be.Name() != "git" {
		t.Fatalf("expected name git, got %q", be.Name())
	}
	ctx := context.Background()
	data := []byte("hello git world")
	meta := SessionMeta{ID: "g1", Title: "git-test", UpdatedAt: time.Now().UTC(), MessageCount: 3}
	if err := be.Push(ctx, "g1", data, meta); err != nil {
		t.Fatalf("push: %v", err)
	}
	got, gotMeta, err := be.Pull(ctx, "g1")
	if err != nil || string(got) != string(data) || gotMeta.Title != "git-test" || gotMeta.MessageCount != 3 {
		t.Fatalf("pull: %q %q %d err=%v", got, gotMeta.Title, gotMeta.MessageCount, err)
	}
	metas, _ := be.List(ctx)
	if len(metas) != 1 {
		t.Fatalf("expected 1 meta got %d", len(metas))
	}
	if err := be.Delete(ctx, "g1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, _, err := be.Pull(ctx, "g1"); err != ErrNotFound {
		t.Fatalf("expected not found after delete, got %v", err)
	}
}

func TestGitBackendConfig(t *testing.T) {
	dir := t.TempDir()
	// Defaults
	be, err := NewGitBackend(config.SyncGitConfig{LocalDir: dir})
	if err != nil || be.branch != "main" {
		t.Fatalf("defaults: branch=%q err=%v", be.branch, err)
	}
	// Custom branch
	dir2 := t.TempDir()
	be2, _ := NewGitBackend(config.SyncGitConfig{LocalDir: dir2, Branch: "develop"})
	if be2.branch != "develop" {
		t.Fatalf("custom branch: got %q", be2.branch)
	}
	// Existing repo
	dir3 := t.TempDir()
	runGit(dir3, "init", "-b", "main")
	be3, err := NewGitBackend(config.SyncGitConfig{LocalDir: dir3})
	if err != nil || be3.branch != "main" {
		t.Fatalf("existing repo: err=%v branch=%q", err, be3.branch)
	}
	// Empty remote
	be4, _ := NewGitBackend(config.SyncGitConfig{LocalDir: dir, RemoteURL: ""})
	if be4.remote != "" {
		t.Fatal("expected empty remote")
	}
}

func TestGitBackendEdgeCases(t *testing.T) {
	dir := t.TempDir()
	be, _ := NewGitBackend(config.SyncGitConfig{LocalDir: dir})
	ctx := context.Background()
	// Pull not found
	if _, _, err := be.Pull(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	// Delete not found
	if err := be.Delete(ctx, "nope"); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	// Delete partial
	os.WriteFile(filepath.Join(dir, "orphan.json.gz"), []byte("d"), 0o644)
	if err := be.Delete(ctx, "orphan"); err != nil {
		t.Fatalf("partial delete: %v", err)
	}
}

func TestGitBackendPullMissingMeta(t *testing.T) {
	dir := t.TempDir()
	be, _ := NewGitBackend(config.SyncGitConfig{LocalDir: dir})
	ctx := context.Background()
	data := []byte("orphan blob")
	be.Push(ctx, "om1", data, SessionMeta{ID: "om1", Title: "orphan", UpdatedAt: time.Now().UTC()})
	os.Remove(filepath.Join(dir, "om1.meta.json"))
	got, gotMeta, err := be.Pull(ctx, "om1")
	if err != nil || string(got) != string(data) || gotMeta.ID != "om1" {
		t.Fatalf("pull without meta: %q id=%q err=%v", got, gotMeta.ID, err)
	}
}

func TestGitBackendListFiltered(t *testing.T) {
	dir := t.TempDir()
	be, _ := NewGitBackend(config.SyncGitConfig{LocalDir: dir})
	ctx := context.Background()
	// Empty list
	if metas, _ := be.List(ctx); len(metas) != 0 {
		t.Fatal("expected empty")
	}
	// Non-meta ignored
	be.Push(ctx, "ls1", []byte("d"), SessionMeta{ID: "ls1", Title: "listed", UpdatedAt: time.Now().UTC()})
	os.WriteFile(filepath.Join(dir, "junk.txt"), []byte("j"), 0o644)
	metas, _ := be.List(ctx)
	if len(metas) != 1 || metas[0].Title != "listed" {
		t.Fatalf("expected 1 meta 'listed', got %d", len(metas))
	}
}

// ---------------------------------------------------------------------------
// Encode / Decode
// ---------------------------------------------------------------------------

func TestEncodeDecodeSession(t *testing.T) {
	t.Parallel()
	s := session.NewSession("/tmp/x")
	s.Title = "test"
	data, err := EncodeSession(s)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	out, err := DecodeSession(data)
	if err != nil || out.Title != "test" || out.ID != s.ID {
		t.Fatalf("roundtrip: %+v err=%v", out, err)
	}
}

func TestDecodeSessionErrors(t *testing.T) {
	t.Parallel()
	if _, err := DecodeSession([]byte("not gzip")); err == nil {
		t.Fatal("expected error for corrupt gzip")
	}
	if _, err := DecodeSession([]byte{}); err == nil {
		t.Fatal("expected error for empty")
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	gz.Write([]byte("not json"))
	gz.Close()
	if _, err := DecodeSession(buf.Bytes()); err == nil {
		t.Fatal("expected error for non-JSON")
	}
}

// ---------------------------------------------------------------------------
// Manager — Resolve
// ---------------------------------------------------------------------------

func TestResolve(t *testing.T) {
	t.Parallel()
	mgr := NewManager(nil, nil)
	older := &session.Session{ID: "x", Title: "old", UpdatedAt: time.Unix(1, 0).UTC()}
	newer := &session.Session{ID: "x", Title: "new", UpdatedAt: time.Unix(100, 0).UTC()}
	// Newer wins
	w, l := mgr.Resolve(older, newer)
	if w.Title != "new" || l == nil || l.Title != "[conflict] old" {
		t.Fatalf("newer wins: w=%q l=%+v", w.Title, l)
	}
	// Reverse args — newer still wins
	w, l = mgr.Resolve(newer, older)
	if w.Title != "new" || l == nil || l.Title != "[conflict] old" {
		t.Fatalf("reversed: w=%q l=%+v", w.Title, l)
	}
	// Nil cases
	if w, l := mgr.Resolve(nil, newer); w.Title != "new" || l != nil {
		t.Fatalf("nil local: w=%+v l=%+v", w, l)
	}
	if w, l := mgr.Resolve(newer, nil); w.Title != "new" || l != nil {
		t.Fatalf("nil remote: w=%+v l=%+v", w, l)
	}
	if w, l := mgr.Resolve(nil, nil); w != nil || l != nil {
		t.Fatal("both nil should return nil,nil")
	}
	// Equal time — local wins, remote becomes conflict
	now := time.Now().UTC()
	w, l = mgr.Resolve(&session.Session{ID: "x", Title: "local", UpdatedAt: now},
		&session.Session{ID: "x", Title: "remote", UpdatedAt: now})
	if w.Title != "local" || l == nil || l.Title != "[conflict] remote" {
		t.Fatalf("equal time: w=%q l=%+v", w.Title, l)
	}
}

// ---------------------------------------------------------------------------
// Manager — Status / Backend
// ---------------------------------------------------------------------------

func TestManagerStatus(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	// Disabled
	mgr := NewManager(store, nil)
	st, err := mgr.Status(context.Background())
	if err != nil || st.Backend != "disabled" || st.Local != 0 || st.Remote != 0 {
		t.Fatalf("disabled: %+v err=%v", st, err)
	}
	// Enabled
	dir := t.TempDir()
	be, _ := NewFileBackend(config.SyncFileConfig{Path: dir})
	mgr2 := NewManager(store, be)
	if mgr2.Backend() != be {
		t.Fatal("Backend() mismatch")
	}
	s := session.NewSession("/tmp/x")
	store.Create(context.Background(), s)
	mgr2.PushOne(context.Background(), s.ID)
	st, _ = mgr2.Status(context.Background())
	if st.Backend != "file" || st.Local != 1 || st.Remote != 1 || st.LastPush.IsZero() {
		t.Fatalf("enabled: %+v", st)
	}
}

// ---------------------------------------------------------------------------
// Manager — Push / Pull
// ---------------------------------------------------------------------------

func TestManagerPushPull(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	be, _ := NewFileBackend(config.SyncFileConfig{Path: dir})
	storeA := newTestStore(t)
	storeB := newTestStore(t)
	ctx := context.Background()

	s := session.NewSession("/tmp/x")
	s.Title = "from A"
	storeA.Create(ctx, s)
	mgrA := NewManager(storeA, be)
	if err := mgrA.PushOne(ctx, s.ID); err != nil {
		t.Fatalf("push: %v", err)
	}
	mgrB := NewManager(storeB, be)
	n, err := mgrB.PullAll(ctx)
	if err != nil || n != 1 {
		t.Fatalf("pullAll: n=%d err=%v", n, err)
	}
	got, _ := storeB.Load(ctx, s.ID)
	if got.Title != "from A" {
		t.Fatalf("title not synced: %q", got.Title)
	}
}

func TestManagerDisabledPaths(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	mgr := NewManager(store, nil)
	if err := mgr.PushOne(context.Background(), "any"); err == nil {
		t.Fatal("expected pushOne error")
	}
	if _, err := mgr.PushAll(context.Background()); err == nil {
		t.Fatal("expected pushAll error")
	}
	if err := mgr.PullOne(context.Background(), "any"); err == nil {
		t.Fatal("expected pullOne error")
	}
	if _, err := mgr.PullAll(context.Background()); err == nil {
		t.Fatal("expected pullAll error")
	}
}

func TestManagerPushOneNotFound(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	be, _ := NewFileBackend(config.SyncFileConfig{Path: dir})
	mgr := NewManager(newTestStore(t), be)
	if err := mgr.PushOne(context.Background(), "bogus"); err == nil {
		t.Fatal("expected error for nonexistent session")
	}
}

func TestManagerPushAll(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	be, _ := NewFileBackend(config.SyncFileConfig{Path: dir})
	store := newTestStore(t)
	mgr := NewManager(store, be)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		s := session.NewSession(fmt.Sprintf("/tmp/%d", i))
		s.Title = fmt.Sprintf("s%d", i)
		store.Create(ctx, s)
	}
	n, err := mgr.PushAll(ctx)
	if err != nil || n != 3 {
		t.Fatalf("pushAll: n=%d err=%v", n, err)
	}
}

func TestManagerPullOneCreateNew(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	be, _ := NewFileBackend(config.SyncFileConfig{Path: dir})
	storeA, storeB := newTestStore(t), newTestStore(t)
	ctx := context.Background()
	s := session.NewSession("/tmp/x")
	s.Title = "remote-only"
	storeA.Create(ctx, s)
	NewManager(storeA, be).PushOne(ctx, s.ID)
	mgrB := NewManager(storeB, be)
	if err := mgrB.PullOne(ctx, s.ID); err != nil {
		t.Fatalf("pullOne create: %v", err)
	}
	if got, _ := storeB.Load(ctx, s.ID); got.Title != "remote-only" {
		t.Fatalf("expected 'remote-only', got %q", got.Title)
	}
}

func TestManagerPullOneConflictRemoteWins(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	be, _ := NewFileBackend(config.SyncFileConfig{Path: dir})
	storeA, storeB := newTestStore(t), newTestStore(t)
	ctx := context.Background()
	s := session.NewSession("/tmp/x")
	s.Title = "original"
	storeA.Create(ctx, s)
	mgrA, mgrB := NewManager(storeA, be), NewManager(storeB, be)
	mgrA.PushOne(ctx, s.ID)
	mgrB.PullOne(ctx, s.ID)
	// A saves first (older)
	as, _ := storeA.Load(ctx, s.ID)
	as.Title = "modified by A (old)"
	storeA.Save(ctx, as)
	time.Sleep(50 * time.Millisecond)
	// B saves and pushes (newer)
	bs, _ := storeB.Load(ctx, s.ID)
	bs.Title = "modified by B (newer)"
	storeB.Save(ctx, bs)
	mgrB.PushOne(ctx, s.ID)
	// A pulls — remote wins
	if err := mgrA.PullOne(ctx, s.ID); err != nil {
		t.Fatalf("pullOne: %v", err)
	}
	winner, _ := storeA.Load(ctx, s.ID)
	if winner.Title != "modified by B (newer)" {
		t.Fatalf("expected remote win, got %q", winner.Title)
	}
	// Conflict copy exists
	locals, _ := storeA.List(ctx, session.ListOptions{})
	found := false
	for _, l := range locals {
		if l.Title == "[conflict] modified by A (old)" {
			found = true
		}
	}
	if !found {
		t.Fatal("conflict copy missing")
	}
}

func TestManagerPullOneLocalWins(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	be, _ := NewFileBackend(config.SyncFileConfig{Path: dir})
	storeA, storeB := newTestStore(t), newTestStore(t)
	ctx := context.Background()
	s := session.NewSession("/tmp/x")
	s.Title = "original"
	storeA.Create(ctx, s)
	mgrA, mgrB := NewManager(storeA, be), NewManager(storeB, be)
	mgrA.PushOne(ctx, s.ID)
	mgrB.PullOne(ctx, s.ID)
	// B saves and pushes first (older)
	bs, _ := storeB.Load(ctx, s.ID)
	bs.Title = "modified by B (older)"
	storeB.Save(ctx, bs)
	mgrB.PushOne(ctx, s.ID)
	time.Sleep(50 * time.Millisecond)
	// A saves later (newer)
	as, _ := storeA.Load(ctx, s.ID)
	as.Title = "modified by A (newer)"
	storeA.Save(ctx, as)
	// A pulls — local wins
	mgrA.PullOne(ctx, s.ID)
	winner, _ := storeA.Load(ctx, s.ID)
	if winner.Title != "modified by A (newer)" {
		t.Fatalf("expected local win, got %q", winner.Title)
	}
}

func TestManagerPullAllDisabled(t *testing.T) {
	t.Parallel()
	_, err := NewManager(newTestStore(t), nil).PullAll(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestManagerPullOneNotFound(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	be, _ := NewFileBackend(config.SyncFileConfig{Path: dir})
	mgr := NewManager(newTestStore(t), be)
	if err := mgr.PullOne(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Concurrency
// ---------------------------------------------------------------------------

func TestManagerConcurrentPushPull(t *testing.T) {
	dir := t.TempDir()
	be, _ := NewFileBackend(config.SyncFileConfig{Path: dir})
	store := newTestStore(t)
	ctx := context.Background()
	const n = 5
	for i := 0; i < n; i++ {
		s := session.NewSession("/tmp/x")
		s.Title = fmt.Sprintf("s%d", i)
		store.Create(ctx, s)
	}
	mgr := NewManager(store, be)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		idx := i
		go func() {
			defer wg.Done()
			locals, _ := store.List(ctx, session.ListOptions{})
			if idx < len(locals) {
				mgr.PushOne(ctx, locals[idx].ID)
			}
		}()
	}
	wg.Wait()
	metas, _ := be.List(ctx)
	if len(metas) != n {
		t.Fatalf("concurrent push: expected %d metas, got %d", n, len(metas))
	}
	store2 := newTestStore(t)
	mgr2 := NewManager(store2, be)
	for i := 0; i < n; i++ {
		wg.Add(1)
		idx := i
		go func() {
			defer wg.Done()
			mgr2.PullOne(ctx, metas[idx].ID)
		}()
	}
	wg.Wait()
	locals2, _ := store2.List(ctx, session.ListOptions{})
	if len(locals2) != n {
		t.Fatalf("concurrent pull: expected %d, got %d", n, len(locals2))
	}
}

func TestManagerConcurrentStatus(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	be, _ := NewFileBackend(config.SyncFileConfig{Path: dir})
	mgr := NewManager(newTestStore(t), be)
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := mgr.Status(context.Background()); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Errorf("concurrent status: %v", e)
	}
}

// ---------------------------------------------------------------------------
// AutoPushIfEnabled
// ---------------------------------------------------------------------------

func TestAutoPushBailouts(t *testing.T) {
	t.Parallel()
	AutoPushIfEnabled(nil, nil, "x", nil)                                                     // nil config
	AutoPushIfEnabled(&config.Config{Sync: config.SyncConfig{AutoPush: true}}, nil, "x", nil) // nil mgr
	cfg := &config.Config{Sync: config.SyncConfig{AutoPush: true}}
	dir := t.TempDir()
	be, _ := NewFileBackend(config.SyncFileConfig{Path: dir})
	mgr := NewManager(newTestStore(t), be)
	AutoPushIfEnabled(cfg, mgr, "", nil)                                                       // empty ID
	AutoPushIfEnabled(&config.Config{Sync: config.SyncConfig{AutoPush: false}}, mgr, "x", nil) // disabled
}

func TestAutoPushEnabled(t *testing.T) {
	dir := t.TempDir()
	be, _ := NewFileBackend(config.SyncFileConfig{Path: dir})
	store := newTestStore(t)
	ctx := context.Background()
	s := session.NewSession("/tmp/x")
	s.Title = "autopush-test"
	store.Create(ctx, s)
	cfg := &config.Config{Sync: config.SyncConfig{AutoPush: true}}
	errCh := make(chan error, 1)
	AutoPushIfEnabled(cfg, NewManager(store, be), s.ID, func(err error) { errCh <- err })
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-errCh:
			t.Fatalf("autopush error: %v", err)
		default:
		}
		if _, meta, err := be.Pull(ctx, s.ID); err == nil && meta.Title == "autopush-test" {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("autopush timeout")
}

func TestAutoPushErrorLogs(t *testing.T) {
	dir := t.TempDir()
	be, _ := NewFileBackend(config.SyncFileConfig{Path: dir})
	cfg := &config.Config{Sync: config.SyncConfig{AutoPush: true}}
	errCh := make(chan error, 1)
	AutoPushIfEnabled(cfg, NewManager(newTestStore(t), be), "nonexistent", func(err error) { errCh <- err })
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("expected error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for error")
	}
}

func TestAutoPushNilLogFn(t *testing.T) {
	dir := t.TempDir()
	be, _ := NewFileBackend(config.SyncFileConfig{Path: dir})
	cfg := &config.Config{Sync: config.SyncConfig{AutoPush: true}}
	AutoPushIfEnabled(cfg, NewManager(newTestStore(t), be), "nonexistent", nil)
	time.Sleep(100 * time.Millisecond) // ensure no panic
}

// ---------------------------------------------------------------------------
// NewBackend dispatch
// ---------------------------------------------------------------------------

func TestNewBackendDispatch(t *testing.T) {
	t.Parallel()
	// Disabled
	if be, err := NewBackend(config.SyncConfig{}); err != nil || be != nil {
		t.Fatalf("disabled: be=%v err=%v", be, err)
	}
	// Unknown
	if _, err := NewBackend(config.SyncConfig{Backend: "bogus"}); err == nil {
		t.Fatal("expected error for unknown backend")
	}
	// File
	be, err := NewBackend(config.SyncConfig{Backend: "file", File: config.SyncFileConfig{Path: t.TempDir()}})
	if err != nil || be == nil || be.Name() != "file" {
		t.Fatalf("file dispatch: %v %v", be, err)
	}
	// Git
	be, err = NewBackend(config.SyncConfig{Backend: "git", Git: config.SyncGitConfig{LocalDir: t.TempDir()}})
	if err != nil || be == nil || be.Name() != "git" {
		t.Fatalf("git dispatch: %v %v", be, err)
	}
	// S3 (will fail on connect but dispatch works)
	be, err = NewBackend(config.SyncConfig{Backend: "s3", S3: config.SyncS3Config{Bucket: "test", Region: "us-east-1"}})
	_ = be
	_ = err
}

// ---------------------------------------------------------------------------
// S3Backend — config validation + helpers (no live S3 needed)
// ---------------------------------------------------------------------------

func TestNewS3BackendValidation(t *testing.T) {
	t.Parallel()
	if _, err := NewS3Backend(config.SyncS3Config{Bucket: ""}); err == nil {
		t.Fatal("expected error for empty bucket")
	}
	// HTTPS endpoint parsing
	_, _ = NewS3Backend(config.SyncS3Config{Bucket: "b", Endpoint: "https://s3.amazonaws.com", Region: "r"})
	// HTTP endpoint parsing
	_, _ = NewS3Backend(config.SyncS3Config{Bucket: "b", Endpoint: "http://localhost:9000"})
	// Default endpoint
	_, _ = NewS3Backend(config.SyncS3Config{Bucket: "b", Region: "r"})
}

func TestS3BackendHelpers(t *testing.T) {
	t.Parallel()
	if (&S3Backend{}).Name() != "s3" {
		t.Fatal("name mismatch")
	}
	noPrefix := &S3Backend{prefix: ""}
	if noPrefix.key("foo") != "foo" || noPrefix.blobKey("abc") != "abc.json.gz" || noPrefix.metaKey("abc") != "abc.meta.json" {
		t.Fatal("noprefix keys wrong")
	}
	withPrefix := &S3Backend{prefix: "overkill/sync"}
	if withPrefix.blobKey("abc") != "overkill/sync/abc.json.gz" || withPrefix.metaKey("abc") != "overkill/sync/abc.meta.json" {
		t.Fatal("prefixed keys wrong")
	}
}

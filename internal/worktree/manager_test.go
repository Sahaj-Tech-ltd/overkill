package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// initRepo creates a minimal git repo in a temp dir + a single
// commit so `git worktree add` has something to branch from.
func initRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "--allow-empty", "-m", "init"},
		// Make sure the branch we'll branch off of has a stable name.
		{"branch", "-M", "main"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, string(out))
		}
	}
	return dir
}

func TestManager_AcquireCreatesWorktreeAndBranch(t *testing.T) {
	repo := initRepo(t)
	m := NewManager(repo, "")
	mt, err := m.Acquire("task-1", AcquireOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if mt.Path == "" || mt.Branch == "" {
		t.Errorf("returned tree should have path + branch: %+v", mt)
	}
	if !strings.Contains(mt.Branch, "task-1") {
		t.Errorf("branch should embed task id: %s", mt.Branch)
	}
	if _, err := os.Stat(mt.Path); err != nil {
		t.Errorf("worktree path should exist: %v", err)
	}
}

func TestManager_AcquireIsIdempotent(t *testing.T) {
	repo := initRepo(t)
	m := NewManager(repo, "")
	first, err := m.Acquire("task-x", AcquireOptions{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := m.Acquire("task-x", AcquireOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if first.Path != second.Path || first.Branch != second.Branch {
		t.Errorf("repeated acquire should return same tree, got %+v vs %+v", first, second)
	}
}

func TestManager_DistinctTasksGetDistinctTrees(t *testing.T) {
	repo := initRepo(t)
	m := NewManager(repo, "")
	a, _ := m.Acquire("alpha", AcquireOptions{})
	b, _ := m.Acquire("beta", AcquireOptions{})
	if a.Path == b.Path {
		t.Errorf("distinct tasks should get distinct paths: %s == %s", a.Path, b.Path)
	}
	if a.Branch == b.Branch {
		t.Errorf("distinct tasks should get distinct branches: %s == %s", a.Branch, b.Branch)
	}
}

func TestManager_ReleaseRemovesWorktree(t *testing.T) {
	repo := initRepo(t)
	m := NewManager(repo, "")
	mt, _ := m.Acquire("gone", AcquireOptions{})
	if err := m.Release("gone", ReleaseOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(mt.Path); !os.IsNotExist(err) {
		t.Errorf("worktree path should be gone: %v", err)
	}
	if m.Get("gone") != nil {
		t.Error("Get should return nil after Release")
	}
}

func TestManager_ReleaseUnknownIsNoOp(t *testing.T) {
	repo := initRepo(t)
	m := NewManager(repo, "")
	if err := m.Release("never-acquired", ReleaseOptions{}); err != nil {
		t.Errorf("unknown release should be no-op, got %v", err)
	}
}

func TestManager_ReleaseDeleteBranch(t *testing.T) {
	repo := initRepo(t)
	m := NewManager(repo, "")
	mt, _ := m.Acquire("delme", AcquireOptions{})
	if err := m.Release("delme", ReleaseOptions{DeleteBranch: true}); err != nil {
		t.Fatal(err)
	}
	// Verify branch is gone.
	cmd := exec.Command("git", "branch", "--list", mt.Branch)
	cmd.Dir = repo
	out, _ := cmd.CombinedOutput()
	if strings.TrimSpace(string(out)) != "" {
		t.Errorf("branch should be deleted, got: %q", string(out))
	}
}

func TestManager_ForceFreshClobbersStaleWorktree(t *testing.T) {
	repo := initRepo(t)
	m := NewManager(repo, "")
	// First acquire to create the tree.
	if _, err := m.Acquire("redo", AcquireOptions{}); err != nil {
		t.Fatal(err)
	}
	// Simulate a process crash: forget the in-memory allocation
	// but leave the on-disk tree alone.
	m.active = map[string]*ManagedTree{}
	// Re-acquire with ForceFresh — should succeed despite the
	// stale tree.
	if _, err := m.Acquire("redo", AcquireOptions{ForceFresh: true}); err != nil {
		t.Fatalf("ForceFresh should clobber stale tree: %v", err)
	}
}

func TestManager_List(t *testing.T) {
	repo := initRepo(t)
	m := NewManager(repo, "")
	_, _ = m.Acquire("a", AcquireOptions{})
	_, _ = m.Acquire("b", AcquireOptions{})
	if got := m.List(); len(got) != 2 {
		t.Errorf("expected 2 active, got %d", len(got))
	}
}

func TestManager_ReclaimDiscoversExistingTrees(t *testing.T) {
	repo := initRepo(t)
	m := NewManager(repo, "")
	mt, _ := m.Acquire("orphan", AcquireOptions{})

	// Simulate daemon restart — drop in-memory state.
	m2 := NewManager(repo, "")
	if err := m2.Reclaim(); err != nil {
		t.Fatal(err)
	}
	reclaimed := m2.Get("orphan")
	if reclaimed == nil {
		t.Fatal("reclaim should rediscover the orphan worktree")
	}
	if reclaimed.Path != mt.Path {
		t.Errorf("reclaimed path mismatch: %s vs %s", reclaimed.Path, mt.Path)
	}
}

func TestSanitizeBranch(t *testing.T) {
	cases := map[string]string{
		"abc":             "abc",
		"task-1":          "task-1",
		"path/with/slash": "path-with-slash",
		"weird name here": "weird-name-here",
		"a/b/c":           "a-b-c",
		"":                "task",
		"---":             "task",
	}
	for in, want := range cases {
		if got := sanitizeBranch(in); got != want {
			t.Errorf("sanitizeBranch(%q) = %q, want %q", in, got, want)
		}
	}
}

// guarantee filepath import lands once go vet runs.
var _ = filepath.Join

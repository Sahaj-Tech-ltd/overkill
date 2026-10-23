package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

func TestManagedRootAndTaskContainment(t *testing.T) {
	repo := initRepo(t)
	for _, sub := range []string{"..", ".", ".git", ".GIT", "nested/tree", filepath.Join(t.TempDir(), "absolute")} {
		if _, err := NewManager(repo, sub).Acquire("task", AcquireOptions{ForceFresh: true}); err == nil {
			t.Fatalf("accepted root %q", sub)
		}
	}
	m := NewManager(repo, "")
	for _, task := range []string{".", "..", "\n", "\x00", ""} {
		if _, err := m.Acquire(task, AcquireOptions{ForceFresh: true}); err == nil {
			t.Fatalf("accepted task %q", task)
		}
	}
	target := t.TempDir()
	if err := os.WriteFile(filepath.Join(target, "keep"), []byte("safe"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, m.root()); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Acquire("task", AcquireOptions{ForceFresh: true}); err == nil {
		t.Fatal("accepted symlink root")
	}
	if _, err := os.Stat(filepath.Join(target, "keep")); err != nil {
		t.Fatal(err)
	}
}

func TestAllocationAliasesRestartAndForeignTrees(t *testing.T) {
	repo := initRepo(t)
	m := NewManager(repo, "space in root")
	var paths = map[string]bool{}
	for _, id := range []string{"a/b", "a-b", "a b", "a\\b"} {
		mt, err := m.Acquire(id, AcquireOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if paths[mt.Path] {
			t.Fatal("alias collision")
		}
		paths[mt.Path] = true
	}
	foreign := filepath.Join(m.root(), "foreign")
	if err := Add(repo, foreign, "foreign"); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(foreign, "nested")
	if err := Add(repo, nested, "nested"); err != nil {
		t.Fatal(err)
	}
	restarted := NewManager(repo, "space in root")
	if err := restarted.Reclaim(); err != nil {
		t.Fatal(err)
	}
	if len(restarted.List()) != 4 {
		t.Fatalf("claimed foreign trees or lost allocations: %+v", restarted.List())
	}
	if restarted.Get("a/b") == nil {
		t.Fatal("lost original task ID")
	}
	tree := restarted.Get("a/b")
	tree.Path = repo
	if err := restarted.Release("a/b", ReleaseOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".git")); err != nil {
		t.Fatal("mutating returned metadata affected manager")
	}
	if err := m.Release("a/b", ReleaseOptions{}); err != nil {
		t.Fatal("double release:", err)
	}
	if err := m.Reclaim(); err != nil {
		t.Fatal(err)
	}
	if m.Get("a/b") != nil {
		t.Fatal("stale allocation returned after refresh")
	}
}

func TestManagedCleanupPreservesDirtyAndForeignIdentity(t *testing.T) {
	for _, kind := range []string{"untracked", "ignored", "modified"} {
		t.Run(kind, func(t *testing.T) {
			repo := initRepo(t)
			if err := os.WriteFile(filepath.Join(repo, "tracked"), []byte("initial"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("ignored\n.overkill-worktrees/\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := runGit(repo, "add", "tracked", ".gitignore"); err != nil {
				t.Fatal(err)
			}
			if _, err := runGit(repo, "-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-m", "tracked"); err != nil {
				t.Fatal(err)
			}
			m := NewManager(repo, "")
			mt, err := m.Acquire("dirty", AcquireOptions{LockReason: "task"})
			if err != nil {
				t.Fatal(err)
			}
			name := kind
			if kind == "modified" {
				name = "tracked"
			}
			file := filepath.Join(mt.Path, name)
			if err := os.WriteFile(file, []byte("user work"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := m.Release("dirty", ReleaseOptions{Force: true, DeleteBranch: true}); err == nil {
				t.Fatal("discarded dirty tree")
			}
			if _, err := NewManager(repo, "").Acquire("dirty", AcquireOptions{ForceFresh: true}); err == nil {
				t.Fatal("force fresh discarded dirty tree")
			}
			if data, err := os.ReadFile(file); err != nil || string(data) != "user work" {
				t.Fatalf("data lost: %s %v", data, err)
			}
		})
	}
	repo := initRepo(t)
	m := NewManager(repo, "")
	mt, err := m.Acquire("changed", AcquireOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(mt.Path, "checkout", "-b", "user-replacement"); err != nil {
		t.Fatal(err)
	}
	if err := m.Release("changed", ReleaseOptions{Force: true}); err == nil {
		t.Fatal("removed foreign branch")
	}
	if err := NewManager(repo, "").Reclaim(); err != nil {
		t.Fatal(err)
	}
}

func TestAddNeverResetsExistingBranch(t *testing.T) {
	repo := initRepo(t)
	if _, err := runGit(repo, "branch", "existing"); err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(repo, "-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "--allow-empty", "-m", "advance main"); err != nil {
		t.Fatal(err)
	}
	before, err := runGit(repo, "rev-parse", "existing")
	if err != nil {
		t.Fatal(err)
	}
	if err := Add(repo, filepath.Join(t.TempDir(), "path with spaces"), "existing"); err != nil {
		t.Fatal(err)
	}
	after, err := runGit(repo, "rev-parse", "existing")
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("existing branch reset")
	}
}

func TestManagersConcurrentAcquire(t *testing.T) {
	repo := initRepo(t)
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	paths := make(chan string, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tree, err := NewManager(repo, "").Acquire("shared", AcquireOptions{})
			if err != nil {
				errs <- err
				return
			}
			paths <- tree.Path
		}()
	}
	wg.Wait()
	close(errs)
	close(paths)
	for err := range errs {
		t.Error(err)
	}
	var first string
	for path := range paths {
		if first != "" && first != path {
			t.Fatal("competing managers allocated twice")
		}
		first = path
	}
	trees, err := List(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(trees) != 2 {
		t.Fatalf("duplicate allocation: %d", len(trees))
	}
}

func TestManagerSubprocessAcquire(t *testing.T) {
	if repo := os.Getenv("OVERKILL_WORKTREE_LOCK_TEST_REPO"); repo != "" {
		if _, err := NewManager(repo, "").Acquire("cross-process", AcquireOptions{}); err != nil {
			t.Fatal(err)
		}
		return
	}
	repo := initRepo(t)
	commands := make([]*exec.Cmd, 4)
	for i := range commands {
		commands[i] = exec.Command(os.Args[0], "-test.run=^TestManagerSubprocessAcquire$")
		commands[i].Env = append(os.Environ(), "OVERKILL_WORKTREE_LOCK_TEST_REPO="+repo)
		if err := commands[i].Start(); err != nil {
			t.Fatal(err)
		}
	}
	for _, command := range commands {
		if err := command.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	trees, err := List(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(trees) != 2 {
		t.Fatalf("duplicate allocation: %d", len(trees))
	}
	m := NewManager(repo, "")
	if err := m.Reclaim(); err != nil {
		t.Fatal(err)
	}
	if m.Get("cross-process") == nil {
		t.Fatal("missing recovered allocation")
	}
}

func TestAllocationSymlinkAndUnownedDirectoryPreserved(t *testing.T) {
	repo := initRepo(t)
	m := NewManager(repo, "")
	if err := m.Reclaim(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(m.root(), allocationName("target"))
	target := t.TempDir()
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Acquire("target", AcquireOptions{ForceFresh: true}); err == nil {
		t.Fatal("accepted symlink allocation")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Acquire("target", AcquireOptions{ForceFresh: true}); err == nil {
		t.Fatal("removed unowned directory")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

func TestReplacementAtSameGitPathIsNotOwned(t *testing.T) {
	repo := initRepo(t)
	m := NewManager(repo, "")
	mt, err := m.Acquire("replaced", AcquireOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := Remove(repo, mt.Path); err != nil {
		t.Fatal(err)
	}
	if err := Add(repo, mt.Path, mt.Branch); err != nil {
		t.Fatal(err)
	}
	if err := m.Release("replaced", ReleaseOptions{Force: true}); err == nil {
		t.Fatal("removed replacement with same path/branch")
	}
	recovered := NewManager(repo, "")
	if err := recovered.Reclaim(); err != nil {
		t.Fatal(err)
	}
	if len(recovered.List()) != 0 {
		t.Fatal("reclaimed replacement allocation")
	}
	if _, err := os.Stat(mt.Path); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteBranchPreservesUnmergedCommits(t *testing.T) {
	repo := initRepo(t)
	m := NewManager(repo, "")
	mt, err := m.Acquire("commits", AcquireOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runGit(mt.Path, "-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "--allow-empty", "-m", "user change"); err != nil {
		t.Fatal(err)
	}
	if err := m.Release("commits", ReleaseOptions{DeleteBranch: true, Force: true}); err == nil {
		t.Fatal("deleted unmerged branch")
	}
	if _, err := runGit(repo, "rev-parse", "--verify", "refs/heads/"+mt.Branch); err != nil {
		t.Fatal("lost unmerged commits", err)
	}
}

func TestExternalWorktreeLockPreserved(t *testing.T) {
	repo := initRepo(t)
	m := NewManager(repo, "")
	mt, err := m.Acquire("locked", AcquireOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := Lock(repo, mt.Path, "user owns this lock"); err != nil {
		t.Fatal(err)
	}
	if err := m.Release("locked", ReleaseOptions{Force: true}); err == nil {
		t.Fatal("removed externally locked tree")
	}
	trees, err := List(repo)
	if err != nil {
		t.Fatal(err)
	}
	for _, tree := range trees {
		if tree.Path == mt.Path {
			if !tree.Locked || tree.LockReason != "user owns this lock" {
				t.Fatal("changed external lock")
			}
			return
		}
	}
	t.Fatal("tree disappeared")
}

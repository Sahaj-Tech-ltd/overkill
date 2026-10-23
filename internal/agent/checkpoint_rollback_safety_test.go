package agent

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func checkpointGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s: %v", args, out, err)
	}
	return strings.TrimSpace(string(out))
}

func TestCheckpointRollbackMixedHistory(t *testing.T) {
	dir, cleanup := initTestRepo(t)
	defer cleanup()
	cm := NewCheckpointManager(dir)
	writeFile(t, dir, "marker", "first")
	first, err := cm.Snapshot("first")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "marker", "ordinary")
	checkpointGit(t, dir, "add", "-A")
	checkpointGit(t, dir, "commit", "-m", "ordinary\n\ncheckpoint: body is not a checkpoint")
	writeFile(t, dir, "marker", "second")
	second, err := cm.Snapshot("second")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "marker", "after")
	checkpointGit(t, dir, "add", "-A")
	checkpointGit(t, dir, "commit", "-m", "ordinary after")
	snaps, err := cm.ListSnapshots()
	if err != nil || len(snaps) != 2 {
		t.Fatalf("snapshots %v: %v", snaps, err)
	}
	if snaps[0].Hash != second || snaps[1].Hash != first {
		t.Fatalf("wrong order: %v", snaps)
	}
	summary, err := cm.Rollback(0)
	if err != nil {
		t.Fatal(err)
	}
	if got := checkpointGit(t, dir, "rev-parse", "HEAD"); got != second {
		t.Fatalf("rollback0 = %s, want %s", got, second)
	}
	if !strings.Contains(summary, "checkpoint 0") {
		t.Fatalf("summary: %s", summary)
	}
	if _, err := cm.Rollback(1); err != nil {
		t.Fatal(err)
	}
	if got := checkpointGit(t, dir, "rev-parse", "HEAD"); got != first {
		t.Fatalf("rollback1 = %s, want %s", got, first)
	}
	listing, err := cm.FormatSnapshots()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(listing, "0. `") {
		t.Fatalf("listing does not use rollback index: %s", listing)
	}
}

func TestCheckpointRollbackPreservesIndexWorktreeAndIgnoredCollisions(t *testing.T) {
	dir, cleanup := initTestRepo(t)
	defer cleanup()
	cm := NewCheckpointManager(dir)
	writeFile(t, dir, "collision", "checkpoint tracked")
	writeFile(t, dir, "staged", "base")
	writeFile(t, dir, "unstaged", "base")
	target, err := cm.Snapshot("target")
	if err != nil {
		t.Fatal(err)
	}
	checkpointGit(t, dir, "rm", "collision")
	writeFile(t, dir, ".gitignore", "collision\ncache/\n")
	checkpointGit(t, dir, "add", "-A")
	checkpointGit(t, dir, "commit", "-m", "ordinary")
	original := checkpointGit(t, dir, "rev-parse", "HEAD")
	writeFile(t, dir, "staged", "staged content")
	checkpointGit(t, dir, "add", "staged")
	writeFile(t, dir, "staged", "unstaged overlay")
	writeFile(t, dir, "unstaged", "unstaged content")
	writeFile(t, dir, "untracked", "untracked content")
	writeFile(t, dir, "collision", "ignored collision")
	if err := os.Mkdir(filepath.Join(dir, "cache"), 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "cache/keep", "unrelated ignored cache")
	cacheBefore, err := os.Stat(filepath.Join(dir, "cache/keep"))
	if err != nil {
		t.Fatal(err)
	}
	beforeStatus := checkpointGit(t, dir, "status", "--porcelain", "--untracked-files=all")
	summary, err := cm.Rollback(0)
	if err != nil {
		t.Fatal(err)
	}
	if got := checkpointGit(t, dir, "rev-parse", "HEAD"); got != target {
		t.Fatalf("HEAD %s want %s", got, target)
	}
	assertCheckpointContent(t, dir, "collision", "checkpoint tracked")
	assertCheckpointContent(t, dir, "cache/keep", "unrelated ignored cache")
	cacheAfter, err := os.Stat(filepath.Join(dir, "cache/keep"))
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(cacheBefore, cacheAfter) {
		t.Fatal("unrelated ignored file was moved or recreated")
	}
	refs := checkpointGit(t, dir, "for-each-ref", "--format=%(refname)", "refs/overkill/rollback/")
	var headRef, worktreeRef string
	for _, ref := range strings.Split(refs, "\n") {
		if strings.HasSuffix(ref, "/head") {
			headRef = ref
		}
		if strings.HasSuffix(ref, "/worktree") {
			worktreeRef = ref
		}
	}
	if headRef == "" || worktreeRef == "" || !strings.Contains(summary, headRef) {
		t.Fatalf("missing recovery refs: %s / %s", refs, summary)
	}
	checkpointGit(t, dir, "reset", "--hard", headRef)
	checkpointGit(t, dir, "stash", "apply", "--index", worktreeRef)
	if got := checkpointGit(t, dir, "rev-parse", "HEAD"); got != original {
		t.Fatalf("recovery HEAD %s", got)
	}
	if got := checkpointGit(t, dir, "status", "--porcelain", "--untracked-files=all"); got != beforeStatus {
		t.Fatalf("status restored %q want %q", got, beforeStatus)
	}
	for p, value := range map[string]string{"staged": "unstaged overlay", "unstaged": "unstaged content", "untracked": "untracked content", "collision": "ignored collision", "cache/keep": "unrelated ignored cache"} {
		assertCheckpointContent(t, dir, p, value)
	}
	if got := checkpointGit(t, dir, "show", ":staged"); got != "staged content" {
		t.Fatalf("staging lost: %s", got)
	}
}

func assertCheckpointContent(t *testing.T, dir, path, want string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, path))
	if err != nil || string(b) != want {
		t.Fatalf("%s = %q, %v; want %q", path, b, err, want)
	}
}

func TestCheckpointRollbackInvalidAndStaleTargets(t *testing.T) {
	for _, test := range []string{"negative", "out-of-range", "invalid-object", "unreachable-object", "cancelled"} {
		t.Run(test, func(t *testing.T) {
			dir, cleanup := initTestRepo(t)
			defer cleanup()
			cm := NewCheckpointManager(dir)
			writeFile(t, dir, "file", "base")
			hash, err := cm.Snapshot("base")
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, dir, "file", "user changes")
			checkpointGit(t, dir, "add", "file")
			writeFile(t, dir, "file", "unstaged")
			head := checkpointGit(t, dir, "rev-parse", "HEAD")
			status := checkpointGit(t, dir, "status", "--porcelain")
			index, ctx := 0, context.Background()
			switch test {
			case "negative":
				index = -1
			case "out-of-range":
				index = 100
			case "cancelled":
				c, cancel := context.WithCancel(ctx)
				cancel()
				ctx = c
			case "invalid-object", "unreachable-object":
				bad := strings.Repeat("a", len(hash))
				if test == "unreachable-object" {
					bad = checkpointGit(t, dir, "commit-tree", checkpointGit(t, dir, "rev-parse", "HEAD^{tree}"), "-m", "checkpoint: unreachable")
				}
				cm.gitRunner = func(ctx context.Context, args ...string) ([]byte, error) {
					if args[0] == "log" {
						return []byte(bad + "\x00checkpoint: stale\x002026-10-21T01:02:03Z\n"), nil
					}
					cmd := exec.CommandContext(ctx, "git", args...)
					cmd.Dir = dir
					return cmd.CombinedOutput()
				}
			}
			if _, err := cm.RollbackContext(ctx, index); err == nil {
				t.Fatal("expected error")
			}
			if checkpointGit(t, dir, "rev-parse", "HEAD") != head || checkpointGit(t, dir, "status", "--porcelain") != status {
				t.Fatal("invalid request changed repository")
			}
			assertCheckpointContent(t, dir, "file", "unstaged")
			if refs := checkpointGit(t, dir, "for-each-ref", "--format=%(refname)", "refs/overkill/rollback/"); refs != "" {
				t.Fatalf("invalid request created recovery ref: %s", refs)
			}
		})
	}
}

func TestCheckpointRollbackRestoresOnCancellationAndResetFailure(t *testing.T) {
	for _, cancelAfterStash := range []bool{true, false} {
		t.Run(map[bool]string{true: "cancel-after-stash", false: "reset-failure"}[cancelAfterStash], func(t *testing.T) {
			dir, cleanup := initTestRepo(t)
			defer cleanup()
			cm := NewCheckpointManager(dir)
			writeFile(t, dir, "file", "target")
			target, err := cm.Snapshot("target")
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, dir, "file", "ordinary")
			checkpointGit(t, dir, "add", "-A")
			checkpointGit(t, dir, "commit", "-m", "ordinary")
			head := checkpointGit(t, dir, "rev-parse", "HEAD")
			writeFile(t, dir, "file", "staged")
			checkpointGit(t, dir, "add", "file")
			writeFile(t, dir, "file", "unstaged")
			writeFile(t, dir, "untracked", "keep")
			beforeStatus := checkpointGit(t, dir, "status", "--porcelain")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cm.gitRunner = func(ctx context.Context, args ...string) ([]byte, error) {
				if !cancelAfterStash && args[0] == "reset" && args[len(args)-1] == target {
					return nil, errors.New("injected reset failure")
				}
				cmd := exec.CommandContext(ctx, "git", args...)
				cmd.Dir = dir
				out, err := cmd.CombinedOutput()
				if cancelAfterStash && args[0] == "stash" && args[1] == "push" {
					cancel()
				}
				return out, err
			}
			_, err = cm.RollbackContext(ctx, 0)
			if err == nil {
				t.Fatal("expected abort")
			}
			if cancelAfterStash && !errors.Is(err, context.Canceled) {
				t.Fatalf("lost cancellation: %v", err)
			}
			if !strings.Contains(err.Error(), "original state restored") {
				t.Fatalf("failed recovery: %v", err)
			}
			if checkpointGit(t, dir, "rev-parse", "HEAD") != head || checkpointGit(t, dir, "status", "--porcelain") != beforeStatus {
				t.Fatal("aborted rollback changed original repository state")
			}
			assertCheckpointContent(t, dir, "file", "unstaged")
			assertCheckpointContent(t, dir, "untracked", "keep")
			if got := checkpointGit(t, dir, "show", ":file"); got != "staged" {
				t.Fatalf("lost index: %s", got)
			}
		})
	}
}

func TestCheckpointRollbackIgnoredFileDirectoryCollisions(t *testing.T) {
	for _, targetDirectory := range []bool{false, true} {
		t.Run(map[bool]string{false: "target-file", true: "target-directory"}[targetDirectory], func(t *testing.T) {
			dir, cleanup := initTestRepo(t)
			defer cleanup()
			cm := NewCheckpointManager(dir)
			if targetDirectory {
				if err := os.Mkdir(filepath.Join(dir, "collision"), 0755); err != nil {
					t.Fatal(err)
				}
				writeFile(t, dir, "collision/child", "target")
			} else {
				writeFile(t, dir, "collision", "target")
			}
			target, err := cm.Snapshot("target")
			if err != nil {
				t.Fatal(err)
			}
			checkpointGit(t, dir, "rm", "-r", "collision")
			writeFile(t, dir, ".gitignore", "collision\nunrelated/\n")
			checkpointGit(t, dir, "add", "-A")
			checkpointGit(t, dir, "commit", "-m", "ordinary")
			if targetDirectory {
				writeFile(t, dir, "collision", "ignored-file")
			} else {
				if err := os.Mkdir(filepath.Join(dir, "collision"), 0755); err != nil {
					t.Fatal(err)
				}
				writeFile(t, dir, "collision/child", "ignored-child")
			}
			if err := os.Mkdir(filepath.Join(dir, "unrelated"), 0755); err != nil {
				t.Fatal(err)
			}
			odd := "unrelated/[literal]* with\nnewline"
			writeFile(t, dir, odd, "untouched")
			before, err := os.Stat(filepath.Join(dir, odd))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := cm.Rollback(0); err != nil {
				t.Fatal(err)
			}
			if checkpointGit(t, dir, "rev-parse", "HEAD") != target {
				t.Fatal("wrong target")
			}
			after, err := os.Stat(filepath.Join(dir, odd))
			if err != nil || !os.SameFile(before, after) {
				t.Fatalf("unrelated ignored path moved: %v", err)
			}
			refs := strings.Split(checkpointGit(t, dir, "for-each-ref", "--format=%(refname)", "refs/overkill/rollback/"), "\n")
			var headRef, stashRef string
			for _, ref := range refs {
				if strings.HasSuffix(ref, "/head") {
					headRef = ref
				}
				if strings.HasSuffix(ref, "/worktree") {
					stashRef = ref
				}
			}
			checkpointGit(t, dir, "reset", "--hard", headRef)
			checkpointGit(t, dir, "stash", "apply", "--index", stashRef)
			if targetDirectory {
				assertCheckpointContent(t, dir, "collision", "ignored-file")
			} else {
				assertCheckpointContent(t, dir, "collision/child", "ignored-child")
			}
		})
	}
}

func TestCheckpointRollbackPreservationFailures(t *testing.T) {
	for _, mode := range []string{"stash-before-save", "stash-after-save", "recovery-ref", "stash-readback"} {
		t.Run(mode, func(t *testing.T) {
			dir, cleanup := initTestRepo(t)
			defer cleanup()
			cm := NewCheckpointManager(dir)
			writeFile(t, dir, "file", "target")
			if _, err := cm.Snapshot("target"); err != nil {
				t.Fatal(err)
			}
			writeFile(t, dir, "file", "staged")
			checkpointGit(t, dir, "add", "file")
			writeFile(t, dir, "file", "unstaged")
			writeFile(t, dir, "untracked", "user")
			head := checkpointGit(t, dir, "rev-parse", "HEAD")
			status := checkpointGit(t, dir, "status", "--porcelain")
			saved := false
			cm.gitRunner = func(ctx context.Context, args ...string) ([]byte, error) {
				if mode == "stash-before-save" && args[0] == "stash" && args[1] == "push" {
					return nil, errors.New("save failed")
				}
				if mode == "recovery-ref" && args[0] == "update-ref" && strings.HasSuffix(args[2], "/worktree") {
					return nil, errors.New("recovery ref failed")
				}
				if mode == "stash-readback" && saved && args[0] == "stash" && args[1] == "list" {
					return nil, errors.New("readback failed")
				}
				cmd := exec.CommandContext(ctx, "git", args...)
				cmd.Dir = dir
				out, err := cmd.CombinedOutput()
				if args[0] == "stash" && args[1] == "push" {
					saved = true
					if mode == "stash-after-save" {
						return out, errors.New("cleanup failed after stash save")
					}
				}
				return out, err
			}
			summary, err := cm.Rollback(0)
			if err == nil {
				t.Fatalf("expected failure: %s", summary)
			}
			if checkpointGit(t, dir, "rev-parse", "HEAD") != head {
				t.Fatal("preservation failure changed HEAD")
			}
			if mode == "stash-readback" {
				// With readback unavailable the code refuses any reset; Git's saved stash
				// still provides a full recovery even though automatic restoration is unsafe.
				checkpointGit(t, dir, "stash", "apply", "--index", "refs/stash")
			}
			if got := checkpointGit(t, dir, "status", "--porcelain"); got != status {
				t.Fatalf("state not restored: %q want %q; error %v", got, status, err)
			}
			assertCheckpointContent(t, dir, "file", "unstaged")
			assertCheckpointContent(t, dir, "untracked", "user")
			if got := checkpointGit(t, dir, "show", ":file"); got != "staged" {
				t.Fatalf("index lost: %s", got)
			}
		})
	}
}

func TestCheckpointListUnbornAndGitFailure(t *testing.T) {
	dir := t.TempDir()
	checkpointGit(t, dir, "init")
	cm := NewCheckpointManager(dir)
	snaps, err := cm.ListSnapshots()
	if err != nil || len(snaps) != 0 {
		t.Fatalf("unborn snapshots: %v %v", snaps, err)
	}
	cm.gitRunner = func(ctx context.Context, args ...string) ([]byte, error) { return nil, errors.New("Git unavailable") }
	if _, err := cm.ListSnapshots(); err == nil {
		t.Fatal("Git failure masked as empty list")
	}
}

func TestCheckpointRollbackFromSubdirectory(t *testing.T) {
	dir, cleanup := initTestRepo(t)
	defer cleanup()
	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "subdir/keep", "tracked")
	writeFile(t, dir, "collision", "target")
	rootManager := NewCheckpointManager(dir)
	target, err := rootManager.Snapshot("target")
	if err != nil {
		t.Fatal(err)
	}
	checkpointGit(t, dir, "rm", "collision")
	writeFile(t, dir, ".gitignore", "collision\ncache/\n")
	checkpointGit(t, dir, "add", "-A")
	checkpointGit(t, dir, "commit", "-m", "ordinary")
	writeFile(t, dir, "collision", "ignored outside working directory")
	if err := os.Mkdir(filepath.Join(dir, "cache"), 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "cache/keep", "untouched")
	cm := NewCheckpointManager(filepath.Join(dir, "subdir"))
	if _, err := cm.Rollback(0); err != nil {
		t.Fatal(err)
	}
	if checkpointGit(t, dir, "rev-parse", "HEAD") != target {
		t.Fatal("wrong target")
	}
	assertCheckpointContent(t, dir, "collision", "target")
	assertCheckpointContent(t, dir, "cache/keep", "untouched")
	stash := checkpointGit(t, dir, "rev-parse", "refs/stash^3:collision")
	if got := checkpointGit(t, dir, "cat-file", "-p", stash); got != "ignored outside working directory" {
		t.Fatalf("ignored content not preserved: %q", got)
	}
}

func TestCheckpointRollbackStagedDeletionWithIgnoredReplacement(t *testing.T) {
	dir, cleanup := initTestRepo(t)
	defer cleanup()
	cm := NewCheckpointManager(dir)
	_, err := cm.Snapshot("before file exists")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "replacement", "tracked ordinary content")
	writeFile(t, dir, ".gitignore", "replacement\n")
	checkpointGit(t, dir, "add", "-f", "replacement")
	checkpointGit(t, dir, "add", ".gitignore")
	checkpointGit(t, dir, "commit", "-m", "ordinary")
	original := checkpointGit(t, dir, "rev-parse", "HEAD")
	checkpointGit(t, dir, "rm", "replacement")
	writeFile(t, dir, "replacement", "ignored replacement user data")
	before := checkpointGit(t, dir, "status", "--porcelain")
	if _, err := cm.Rollback(0); err == nil || !strings.Contains(err.Error(), "untracked replacement") {
		t.Fatalf("expected safe refusal: %v", err)
	}
	if checkpointGit(t, dir, "rev-parse", "HEAD") != original {
		t.Fatal("refusal changed HEAD")
	}
	assertCheckpointContent(t, dir, "replacement", "ignored replacement user data")
	if got := checkpointGit(t, dir, "status", "--porcelain"); got != before {
		t.Fatalf("staged deletion changed: %q want %q", got, before)
	}
	if refs := checkpointGit(t, dir, "for-each-ref", "--format=%(refname)", "refs/overkill/rollback/"); refs != "" {
		t.Fatalf("refusal should precede mutations: %s", refs)
	}

}

func TestCheckpointRollbackRefusesIndexedFileDirectoryReplacement(t *testing.T) {
	dir, cleanup := initTestRepo(t)
	defer cleanup()
	cm := NewCheckpointManager(dir)
	if _, err := cm.Snapshot("target"); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "new-file", "staged")
	checkpointGit(t, dir, "add", "new-file")
	if err := os.Remove(filepath.Join(dir, "new-file")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "new-file"), 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "new-file/untracked", "keep")
	status := checkpointGit(t, dir, "status", "--porcelain")
	if _, err := cm.Rollback(0); err == nil || !strings.Contains(err.Error(), "untracked replacement") {
		t.Fatalf("expected safe refusal: %v", err)
	}
	if got := checkpointGit(t, dir, "status", "--porcelain"); got != status {
		t.Fatalf("refusal changed state: %q want %q", got, status)
	}
	assertCheckpointContent(t, dir, "new-file/untracked", "keep")
	if got := checkpointGit(t, dir, "show", ":new-file"); got != "staged" {
		t.Fatalf("staged data lost: %q", got)
	}
}

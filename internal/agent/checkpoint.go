package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Snapshot represents one git-based filesystem checkpoint.
type Snapshot struct {
	Hash    string
	Message string
	Time    time.Time
}

// CheckpointManager uses git to create, list, and roll back filesystem
// snapshots in a working directory. It's wired into the agent for the
// /snapshot and /rollback slash commands, and optionally for automatic
// pre-tool checkpoints.
type CheckpointManager struct {
	mu      sync.Mutex
	workDir string
	// gitRunner is a test seam; production always invokes Git directly.
	gitRunner func(context.Context, ...string) ([]byte, error)
}

// NewCheckpointManager returns a CheckpointManager rooted at workDir.
// No git operations are performed until Snapshot/Rollback/ListSnapshots
// is called.
func NewCheckpointManager(workDir string) *CheckpointManager {
	return &CheckpointManager{workDir: workDir}
}

// WorkDir returns the working directory this manager operates on.
func (cm *CheckpointManager) WorkDir() string { return cm.workDir }

// Snapshot stages all changes (git add -A) and commits them with the
// message "checkpoint: <name>". Returns the new commit hash.
func (cm *CheckpointManager) Snapshot(name string) (string, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if err := cm.verifyRepo(); err != nil {
		return "", err
	}

	// Stage everything.
	add := exec.Command("git", "add", "-A")
	add.Dir = cm.workDir
	if out, err := add.CombinedOutput(); err != nil {
		return "", fmt.Errorf("git add -A: %s: %w", strings.TrimSpace(string(out)), err)
	}

	// Commit.
	msg := "checkpoint: " + name
	commit := exec.Command("git", "commit", "--allow-empty", "-m", msg)
	commit.Dir = cm.workDir
	if out, err := commit.CombinedOutput(); err != nil {
		return "", fmt.Errorf("git commit: %s: %w", strings.TrimSpace(string(out)), err)
	}

	// Return the new HEAD hash.
	hash := exec.Command("git", "rev-parse", "HEAD")
	hash.Dir = cm.workDir
	hashOut, err := hash.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git rev-parse: %s: %w", strings.TrimSpace(string(hashOut)), err)
	}
	return strings.TrimSpace(string(hashOut)), nil
}

// Rollback restores the Nth listed checkpoint (zero is the latest). Both the
// original HEAD and dirty index/worktree are retained for recovery.
func (cm *CheckpointManager) Rollback(n int) (string, error) {
	return cm.RollbackContext(context.Background(), n)
}

// RollbackContext observes cancellation before resetting. Once preservation or
// reset starts, Git is allowed to finish so cancellation cannot kill it halfway
// through an index/worktree update. Recovery artifacts are deliberately retained.
func (cm *CheckpointManager) RollbackContext(ctx context.Context, n int) (string, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if n < 0 {
		return "", fmt.Errorf("checkpoint index must be non-negative")
	}
	snaps, err := cm.listSnapshots(ctx)
	if err != nil {
		return "", err
	}
	if n >= len(snaps) {
		return "", fmt.Errorf("checkpoint index %d out of range: %d checkpoint(s)", n, len(snaps))
	}
	target := snaps[n].Hash
	if _, err := cm.git(ctx, "cat-file", "-e", target+"^{commit}"); err != nil {
		return "", fmt.Errorf("invalid checkpoint %s: %w", target, err)
	}
	original, err := cm.git(ctx, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return "", err
	}
	head := strings.TrimSpace(string(original))
	if _, err := cm.git(ctx, "merge-base", "--is-ancestor", target, head); err != nil {
		return "", fmt.Errorf("checkpoint %s is no longer reachable from HEAD: %w", target, err)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := cm.validateStashRecovery(ctx); err != nil {
		return "", err
	}
	// A durable ref retains ordinary commits that the reset would otherwise orphan.
	base := fmt.Sprintf("refs/overkill/rollback/%d", time.Now().UnixNano())
	headRef := base + "/head"
	if _, err := cm.git(context.Background(), "update-ref", "--create-reflog", headRef, head, ""); err != nil {
		return "", fmt.Errorf("preserve rollback HEAD: %w", err)
	}
	recovery := "original HEAD saved at " + headRef + " (git reset --hard " + headRef + ")"
	before, err := cm.git(ctx, "stash", "list", "--format=%H", "-n", "1")
	if err != nil {
		return "", fmt.Errorf("read recovery stash before rollback: %w", err)
	}
	// Exclude unrelated ignored files, keeping dependency/cache trees in place.
	exclusions, _, err := cm.ignoredRollbackPaths(ctx, target)
	if err != nil {
		return "", err
	}
	pathspec, err := os.CreateTemp("", "overkill-rollback-pathspec-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(pathspec.Name())
	var specs strings.Builder
	specs.WriteString(":/\x00")
	for _, path := range exclusions {
		specs.WriteString(":(top,exclude,literal)" + path + "\x00")
	}
	if _, err := pathspec.WriteString(specs.String()); err != nil {
		pathspec.Close()
		return "", err
	}
	if err := pathspec.Close(); err != nil {
		return "", err
	}
	_, preserveErr := cm.git(context.Background(), "stash", "push", "--all", "--pathspec-from-file="+pathspec.Name(), "--pathspec-file-nul", "-m", "overkill rollback recovery "+headRef)
	after, err := cm.git(context.Background(), "stash", "list", "--format=%H", "-n", "1")
	if err != nil {
		return "", fmt.Errorf("cannot verify preserved worktree; refusing reset; %s; inspect refs/stash for recovery: %w", recovery, err)
	}
	stash := ""
	if strings.TrimSpace(string(after)) != strings.TrimSpace(string(before)) {
		stash = strings.TrimSpace(string(after))
		saved, err := cm.git(context.Background(), "show", "-s", "--format=%P%x00%s", stash)
		parts := strings.SplitN(strings.TrimSpace(string(saved)), "\x00", 2)
		if err != nil || len(parts) != 2 || !strings.HasPrefix(parts[0], head+" ") || !strings.HasSuffix(parts[1], "overkill rollback recovery "+headRef) {
			return "", fmt.Errorf("cannot identify this rollback's stash; refusing reset; %s; inspect git stash list for recovery", recovery)
		}
		recovery += "; worktree/index saved at " + stash + " (restore HEAD as above, then git stash apply --index " + stash + ")"
		if _, err := cm.git(context.Background(), "update-ref", "--create-reflog", base+"/worktree", stash, ""); err != nil {
			return "", cm.recoverRollback(head, stash, recovery, fmt.Errorf("preserve worktree recovery ref: %w", err), false)
		}
	}
	if preserveErr != nil {
		if stash != "" {
			return "", cm.recoverRollback(head, stash, recovery, preserveErr, false)
		}
		return "", fmt.Errorf("preserve worktree before rollback (%s): %w", recovery, preserveErr)
	}
	// Git cannot stash dirty submodules or nested repositories. Do not reset when
	// preservation left anything behind, even if the target appears harmless.
	dirty, err := cm.git(context.Background(), "status", "--porcelain", "--untracked-files=all", "--ignore-submodules=none")
	if err != nil {
		return "", cm.recoverRollback(head, stash, recovery, err, false)
	}
	if strings.TrimSpace(string(dirty)) != "" {
		return "", cm.recoverRollback(head, stash, recovery, fmt.Errorf("worktree contains changes Git could not preserve; refusing rollback"), false)
	}
	_, collisions, err := cm.ignoredRollbackPaths(context.Background(), target)
	if err != nil {
		return "", cm.recoverRollback(head, stash, recovery, err, false)
	}
	if len(collisions) != 0 {
		return "", cm.recoverRollback(head, stash, recovery, fmt.Errorf("ignored paths could not be preserved; refusing rollback"), false)
	}
	current, err := cm.git(context.Background(), "rev-parse", "--verify", "HEAD")
	if err != nil {
		return "", cm.recoverRollback(head, stash, recovery, err, false)
	}
	if strings.TrimSpace(string(current)) != head {
		// An external Git operation changed HEAD. Never reset its newly selected work.
		return "", fmt.Errorf("HEAD changed during rollback; refusing reset; %s", recovery)
	}
	if err := ctx.Err(); err != nil {
		return "", cm.recoverRollback(head, stash, recovery, err, false)
	}
	if _, err := cm.git(context.Background(), "reset", "--hard", "--no-recurse-submodules", target); err != nil {
		return "", cm.recoverRollback(head, stash, recovery, err, true)
	}
	return fmt.Sprintf("rolled back to checkpoint %d; now at %s; %s", n, target, recovery), nil
}

// Git stash --index cannot reliably restore an untracked replacement of a
// staged deletion or an index file replaced by a directory: its untracked
// checkout collides with the tracked path.
// Refuse this state before moving any files rather than promising broken recovery.
func (cm *CheckpointManager) validateStashRecovery(ctx context.Context) error {
	head, err := cm.git(ctx, "ls-tree", "-r", "--full-tree", "--name-only", "-z", "HEAD")
	if err != nil {
		return err
	}
	index, err := cm.git(ctx, "ls-files", "--cached", "--full-name", "-z", "--", ":/")
	if err != nil {
		return err
	}
	indexed := map[string]bool{}
	for _, path := range strings.Split(string(index), "\x00") {
		indexed[path] = true
	}
	missing, dirs := map[string]bool{}, map[string]bool{}
	// Indexed paths can also conflict with an untracked file/directory replacement.
	for path := range indexed {
		if path != "" {
			missing[path] = true
		}
	}
	for _, path := range strings.Split(string(head), "\x00") {
		if path == "" || indexed[path] {
			continue
		}
		missing[path] = true
		for i := strings.LastIndex(path, "/"); i >= 0; i = strings.LastIndex(path[:i], "/") {
			dirs[path[:i]] = true
		}
	}
	for path := range missing {
		for i := strings.LastIndex(path, "/"); i >= 0; i = strings.LastIndex(path[:i], "/") {
			dirs[path[:i]] = true
		}
	}
	others, err := cm.git(ctx, "ls-files", "--others", "--full-name", "-z", "--", ":/")
	if err != nil {
		return err
	}
	for _, path := range strings.Split(string(others), "\x00") {
		if path == "" {
			continue
		}
		collision := missing[path] || dirs[path]
		for i := strings.LastIndex(path, "/"); !collision && i >= 0; i = strings.LastIndex(path[:i], "/") {
			collision = missing[path[:i]]
		}
		if collision {
			return fmt.Errorf("cannot safely restore staged/indexed path with untracked replacement %q; commit or unstage it before rollback", path)
		}
	}
	return nil
}

// ignoredRollbackPaths separates ignored files that conflict with a target
// tracked path from unrelated ignored files. Prefix checks cover file/directory
// replacements in both directions; paths are Git's NUL-delimited root paths.
func (cm *CheckpointManager) ignoredRollbackPaths(ctx context.Context, target string) (unrelated, collisions []string, err error) {
	tree, err := cm.git(ctx, "ls-tree", "-r", "--full-tree", "--name-only", "-z", target)
	if err != nil {
		return nil, nil, err
	}
	// Stash cleanup also returns tracked paths to HEAD. A staged deletion
	// followed by an ignored replacement must be captured even if target lacks it.
	headTree, err := cm.git(ctx, "ls-tree", "-r", "--full-tree", "--name-only", "-z", "HEAD")
	if err != nil {
		return nil, nil, err
	}
	index, err := cm.git(ctx, "ls-files", "--cached", "--full-name", "-z", "--", ":/")
	if err != nil {
		return nil, nil, err
	}
	files, dirs := map[string]bool{}, map[string]bool{}
	allPaths := string(tree) + string(headTree) + string(index)
	for _, path := range strings.Split(allPaths, "\x00") {
		if path == "" {
			continue
		}
		files[path] = true
		for i := strings.LastIndex(path, "/"); i >= 0; i = strings.LastIndex(path[:i], "/") {
			dirs[path[:i]] = true
		}
	}
	ignored, err := cm.git(ctx, "ls-files", "--others", "--ignored", "--exclude-standard", "--full-name", "-z", "--", ":/")
	if err != nil {
		return nil, nil, err
	}
	for _, path := range strings.Split(string(ignored), "\x00") {
		if path == "" {
			continue
		}
		collision := files[path] || dirs[path]
		for i := strings.LastIndex(path, "/"); !collision && i >= 0; i = strings.LastIndex(path[:i], "/") {
			collision = files[path[:i]]
		}
		if collision {
			collisions = append(collisions, path)
		} else {
			unrelated = append(unrelated, path)
		}
	}
	return unrelated, collisions, nil
}

// recoverRollback restores the original index/worktree after a failed or
// cancelled reset. Even if recovery fails, the named refs and stash remain.
func (cm *CheckpointManager) recoverRollback(head, stash, recovery string, cause error, resetAttempted bool) error {
	if resetAttempted {
		if _, err := cm.git(context.Background(), "reset", "--hard", "--no-recurse-submodules", head); err != nil {
			return fmt.Errorf("rollback aborted: %w; recovery reset failed: %v; %s", cause, err, recovery)
		}
	}
	if stash != "" {
		if _, err := cm.git(context.Background(), "stash", "apply", "--index", stash); err != nil {
			return fmt.Errorf("rollback aborted: %w; recovery apply failed: %v; %s", cause, err, recovery)
		}
	}
	return fmt.Errorf("rollback aborted; original state restored; %s: %w", recovery, cause)
}

func (cm *CheckpointManager) git(ctx context.Context, args ...string) ([]byte, error) {
	if cm.gitRunner != nil {
		return cm.gitRunner(ctx, args...)
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = cm.workDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("git %s: %s: %w", strings.Join(args, " "), strings.TrimSpace(string(out)), err)
	}
	return out, nil
}

// ListSnapshots returns checkpoint commits reachable from HEAD, newest first.
// Only a subject beginning with "checkpoint: " identifies a checkpoint.
func (cm *CheckpointManager) ListSnapshots() ([]Snapshot, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	return cm.listSnapshots(context.Background())
}

func (cm *CheckpointManager) listSnapshots(ctx context.Context) ([]Snapshot, error) {
	if _, err := cm.git(ctx, "rev-parse", "--git-dir"); err != nil {
		return nil, err
	}
	if _, err := cm.git(ctx, "rev-parse", "--verify", "HEAD"); err != nil {
		if branch, symbolicErr := cm.git(ctx, "symbolic-ref", "-q", "HEAD"); symbolicErr == nil {
			_, refErr := cm.git(ctx, "show-ref", "--verify", "--quiet", strings.TrimSpace(string(branch)))
			var exit *exec.ExitError
			if errors.As(refErr, &exit) && exit.ExitCode() == 1 {
				return nil, nil
			}
		}
		return nil, err
	}
	out, err := cm.git(ctx, "log", "--format=%H%x00%s%x00%aI", "HEAD", "--")
	if err != nil {
		return nil, err
	}
	var snaps []Snapshot
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Split(line, "\x00")
		if len(fields) != 3 || !strings.HasPrefix(fields[1], "checkpoint: ") {
			continue
		}
		timestamp, err := time.Parse(time.RFC3339, fields[2])
		if err != nil {
			return nil, fmt.Errorf("invalid checkpoint timestamp for %s: %w", fields[0], err)
		}
		snaps = append(snaps, Snapshot{Hash: fields[0], Message: fields[1], Time: timestamp})
	}
	return snaps, nil
}

// FormatSnapshots returns a human-readable listing suitable for display
// in chat. Returns an empty string with no error when there are no
// checkpoints.
func (cm *CheckpointManager) FormatSnapshots() (string, error) {
	snaps, err := cm.ListSnapshots()
	if err != nil {
		return "", err
	}
	if len(snaps) == 0 {
		return "no checkpoints yet — use /snapshot [name] to create one.", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d checkpoint(s):\n", len(snaps))
	for i, s := range snaps {
		short := s.Hash
		if len(short) > 7 {
			short = short[:7]
		}
		ts := ""
		if !s.Time.IsZero() {
			ts = s.Time.Format("2006-01-02 15:04")
		}
		fmt.Fprintf(&b, "%d. `%s`  %s  %s\n", i, short, s.Message, ts)
	}
	b.WriteString("\n/rollback N restores checkpoint N from this zero-based list (0 = latest).")
	return b.String(), nil
}

// verifyRepo checks that workDir is inside a git repository.
func (cm *CheckpointManager) verifyRepo() error {
	cmd := exec.Command("git", "rev-parse", "--git-dir")
	cmd.Dir = cm.workDir
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("not a git repository (or git not installed): %s: %w", cm.workDir, err)
	}
	return nil
}

// riskyToolNames is the set of tool names the checkpoint manager will
// auto-snapshot before. Conservative: false positives cost a commit;
// false negatives lose a rollback target.
var riskyToolNames = map[string]bool{
	"shell":           true,
	"terminal":        true,
	"write_file":      true,
	"patch":           true,
	"fs_write":        true,
	"fs_edit":         true,
	"fs_delete":       true,
	"pty_shell":       true,
	"execute_command": true,
}

// IsRisky returns true when toolName is a destructive tool that should
// trigger an auto-snapshot before execution.
func IsRisky(toolName string) bool {
	return riskyToolNames[toolName]
}

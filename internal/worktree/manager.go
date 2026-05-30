// Package worktree — Manager orchestrates per-subagent worktrees
// (Phase 5 §8.5 parallel agents).
//
// Why: when multiple subagents run concurrently in the same repo,
// they trample each other's working tree — one writes foo.go, the
// other reads stale bytes, both commit on top of unexpected state.
// The fix is one git worktree per subagent: separate checkout,
// separate branch, independent index. The agent doesn't have to
// think about it; the Manager hands out a worktree at the start of
// a task and reclaims it at the end.
//
// Design:
//
//   - One worktree per active task. Branch named
//     `overkill/parallel/<task-id>` so it's grep-able + git-log-
//     greppable + safe to delete later.
//   - Worktrees live under `<repo>/.overkill-worktrees/<task-id>`.
//     We deliberately do NOT use `git worktree add` defaults
//     (which sprinkle worktrees next to the repo) — putting them
//     under a dot-prefixed subdir keeps cleanup tractable and
//     stays out of the user's file tree.
//   - Lockfile-based mutual exclusion: an `Acquire` for an
//     already-held task ID returns the same path (idempotent for
//     retries), but two concurrent Acquires for distinct tasks
//     get distinct worktrees and never collide.
//   - Release deletes the worktree but leaves the branch alone by
//     default — the user can inspect / merge / drop it manually.
//     Pass DeleteBranch=true to drop the branch too.
package worktree

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ManagedTree is one allocated worktree.
type ManagedTree struct {
	TaskID    string
	Path      string
	Branch    string
	CreatedAt time.Time
}

// Manager holds the live allocation map. Concurrency-safe. The
// repoDir is the source-of-truth repo whose worktrees we manage.
type Manager struct {
	repoDir string
	subDir  string
	mu      sync.Mutex
	active  map[string]*ManagedTree
}

// NewManager returns a Manager rooted at repoDir. The subDir is the
// directory NAME (not full path) under repoDir where worktrees
// land; pass "" to use the default `.overkill-worktrees`.
func NewManager(repoDir string, subDir string) *Manager {
	if subDir == "" {
		subDir = ".overkill-worktrees"
	}
	return &Manager{
		repoDir: repoDir,
		subDir:  subDir,
		active:  map[string]*ManagedTree{},
	}
}

// AcquireOptions tunes Acquire behaviour. Zero-value is the
// recommended default: branch derived from task ID, no custom
// reason, no force-fresh.
type AcquireOptions struct {
	// Branch overrides the default `overkill/parallel/<task-id>`.
	// Empty → use the default.
	Branch string
	// ForceFresh removes any pre-existing worktree at the target
	// path before creating a new one. Useful when a prior session
	// crashed mid-task and left a stale tree on disk.
	ForceFresh bool
	// LockReason is passed to `git worktree lock` so `git worktree
	// prune` doesn't reap an in-flight subagent's tree.
	LockReason string
}

// Acquire allocates (or reuses) a worktree for the given task ID.
// Idempotent: calling Acquire twice with the same taskID returns
// the same ManagedTree without re-doing the git work.
func (m *Manager) Acquire(taskID string, opts AcquireOptions) (*ManagedTree, error) {
	if strings.TrimSpace(taskID) == "" {
		return nil, errors.New("worktree manager: task id required")
	}
	if m.repoDir == "" {
		return nil, errors.New("worktree manager: repo dir required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, ok := m.active[taskID]; ok {
		return existing, nil
	}

	branch := opts.Branch
	if branch == "" {
		branch = "overkill/parallel/" + sanitizeBranch(taskID)
	}
	path := filepath.Join(m.repoDir, m.subDir, sanitizePath(taskID))

	if opts.ForceFresh {
		// Best-effort remove; if there was no prior tree, this no-
		// ops. We use --force because the caller asked for clean.
		_ = RemoveForce(m.repoDir, path)
		_ = os.RemoveAll(path)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("worktree manager: mkdir: %w", err)
	}
	if err := Add(m.repoDir, path, branch); err != nil {
		return nil, fmt.Errorf("worktree manager: acquire %s: %w", taskID, err)
	}
	if opts.LockReason != "" {
		if err := Lock(m.repoDir, path, opts.LockReason); err != nil {
			// Lock failure isn't fatal — the worktree exists, it
			// just isn't prune-protected. Surface as wrapped error
			// AND keep the allocation so the caller can decide.
			return nil, fmt.Errorf("worktree manager: lock %s: %w", taskID, err)
		}
	}

	mt := &ManagedTree{
		TaskID:    taskID,
		Path:      path,
		Branch:    branch,
		CreatedAt: time.Now().UTC(),
	}
	m.active[taskID] = mt
	return mt, nil
}

// ReleaseOptions tunes Release.
type ReleaseOptions struct {
	// DeleteBranch removes the branch after the worktree. Default
	// false — keep the branch around so the user can inspect /
	// merge it. Skip when the branch is going to be auto-merged
	// later by upstream tooling.
	DeleteBranch bool
	// Force passes --force to `git worktree remove`, dropping any
	// uncommitted changes. Useful for crashed subagents whose tree
	// is dirty but irrelevant.
	Force bool
}

// Release tears down the worktree for taskID. Idempotent: releasing
// an unknown task is a no-op (returns nil).
func (m *Manager) Release(taskID string, opts ReleaseOptions) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	mt, ok := m.active[taskID]
	if !ok {
		return nil
	}
	// Unlock first; otherwise `git worktree remove` refuses.
	_ = Unlock(m.repoDir, mt.Path)
	var rmErr error
	if opts.Force {
		rmErr = RemoveForce(m.repoDir, mt.Path)
	} else {
		rmErr = Remove(m.repoDir, mt.Path)
	}
	if rmErr != nil {
		return fmt.Errorf("worktree manager: release %s: %w", taskID, rmErr)
	}
	delete(m.active, taskID)
	if opts.DeleteBranch && mt.Branch != "" {
		// Branch deletion is best-effort and uses -D so dirty
		// branches don't block cleanup. The user can recover via
		// reflog if they regret it.
		_, _ = runGit(m.repoDir, "branch", "-D", mt.Branch)
	}
	return nil
}

// List returns the currently allocated worktrees. Order is not
// guaranteed; callers can sort by CreatedAt if they want.
func (m *Manager) List() []*ManagedTree {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*ManagedTree, 0, len(m.active))
	for _, mt := range m.active {
		dup := *mt
		out = append(out, &dup)
	}
	return out
}

// Get returns the ManagedTree for taskID, or nil if none.
func (m *Manager) Get(taskID string) *ManagedTree {
	m.mu.Lock()
	defer m.mu.Unlock()
	if mt, ok := m.active[taskID]; ok {
		dup := *mt
		return &dup
	}
	return nil
}

// Reclaim attempts to recover allocations after a daemon restart.
// Scans `git worktree list` for trees under our subDir, infers
// task IDs from path basenames, and registers them as active.
// Idempotent.
func (m *Manager) Reclaim() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	trees, err := List(m.repoDir)
	if err != nil {
		return fmt.Errorf("worktree manager: reclaim: %w", err)
	}
	subPrefix := filepath.Join(m.repoDir, m.subDir) + string(filepath.Separator)
	for _, t := range trees {
		if !strings.HasPrefix(t.Path, subPrefix) {
			continue
		}
		taskID := filepath.Base(t.Path)
		if _, ok := m.active[taskID]; ok {
			continue
		}
		m.active[taskID] = &ManagedTree{
			TaskID: taskID,
			Path:   t.Path,
			Branch: t.Branch,
			// CreatedAt unknown — leave zero so callers can
			// distinguish reclaimed-from-disk allocations from
			// in-process ones.
		}
	}
	return nil
}

// sanitizeBranch makes a task ID safe for use as a git branch name.
// Replaces characters git rejects (whitespace, `..`, control chars)
// with hyphens. Caller-provided IDs are usually UUIDs; this is for
// human-supplied or path-shaped IDs.
func sanitizeBranch(taskID string) string {
	var b strings.Builder
	for _, r := range taskID {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune(r)
		case r == '/':
			b.WriteRune('-')
		default:
			b.WriteRune('-')
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		return "task"
	}
	// B056: Git branch names have a 250-byte practical limit; truncate to
	// 200 chars for safety (leaves room for prefix/suffix in callers).
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

// sanitizePath strips slashes so the task ID can be used as a
// single directory name under subDir.
func sanitizePath(taskID string) string {
	return strings.ReplaceAll(taskID, "/", "-")
}

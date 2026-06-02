// Package automation — religious commits per stage (master plan §4.8).
//
// AutoCommitter runs `git add -A && git commit -m <msg>` after named
// stages (test-pass, build-green, lint-clean) so the user can `git reset`
// to any milestone without losing intermediate work.
//
// Opt-in (off by default). The agent's stage hooks call Commit(stageName);
// the user enables/disables specific stages via SetEnabled.
package automation

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Stage names that the agent / wave runners can fire commits at.
const (
	StageTestPass     = "test-pass"
	StageBuildGreen   = "build-green"
	StageLintClean    = "lint-clean"
	StagePatchApplied = "patch-applied"
)

// CommitRunner runs `git -C dir add -A && git -C dir commit -m msg`.
// Returned bool reports whether the commit produced output (false = nothing
// to commit, treated as success).
type CommitRunner func(ctx context.Context, dir, msg string) (committed bool, err error)

// DefaultCommitRunner shells out to git.
func DefaultCommitRunner(ctx context.Context, dir, msg string) (bool, error) {
	add := exec.CommandContext(ctx, "git", "add", "-A")
	add.Dir = dir
	if out, err := add.CombinedOutput(); err != nil {
		return false, fmt.Errorf("git add: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	commit := exec.CommandContext(ctx, "git", "commit", "-m", msg, "--allow-empty=false")
	commit.Dir = dir
	out, err := commit.CombinedOutput()
	if err != nil {
		// git commit returns 1 with "nothing to commit" — distinguish that
		// from a real failure.
		if strings.Contains(string(out), "nothing to commit") {
			return false, nil
		}
		return false, fmt.Errorf("git commit: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return true, nil
}

// AutoCommitter is the per-session committer.
type AutoCommitter struct {
	mu      sync.Mutex
	repoDir string
	runner  CommitRunner
	enabled map[string]bool
}

// NewAutoCommitter wires a runner and a per-stage enable map. Pass nil
// runner to use DefaultCommitRunner. Pass nil enabled map to start with
// all stages disabled (caller flips on what they want).
func NewAutoCommitter(repoDir string, runner CommitRunner, enabled map[string]bool) *AutoCommitter {
	if runner == nil {
		runner = DefaultCommitRunner
	}
	if enabled == nil {
		enabled = map[string]bool{}
	}
	return &AutoCommitter{repoDir: repoDir, runner: runner, enabled: enabled}
}

// SetEnabled flips an individual stage.
func (c *AutoCommitter) SetEnabled(stage string, on bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.enabled[stage] = on
}

// Enabled reports whether a stage is set to fire.
func (c *AutoCommitter) Enabled(stage string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.enabled[stage]
}

// Commit runs the commit if the stage is enabled. Skips silently otherwise.
// Returns (committed, error). committed=false with nil err means
// "stage disabled" or "nothing changed".
func (c *AutoCommitter) Commit(ctx context.Context, stage, summary string) (bool, error) {
	if !c.Enabled(stage) {
		return false, nil
	}
	msg := fmt.Sprintf("autocommit(%s): %s", stage, summary)
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return c.runner(cctx, c.repoDir, msg)
}

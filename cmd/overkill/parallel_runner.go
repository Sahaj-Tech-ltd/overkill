package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync"

	"github.com/Sahaj-Tech-ltd/overkill/internal/subagent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/worktree"
)

// parallelRunner pairs the worktree manager with the subagent
// manager so each spawned contract runs inside its own git worktree
// (§8.5). The pairing lives in cmd/overkill — neither internal/
// worktree nor internal/subagent imports the other, keeping the
// dependency graph clean.
//
// Lifecycle:
//
//  1. SpawnInWorktree(ctx, contract) acquires a worktree keyed by
//     contract.ID and hands its path to subagent.SpawnFromFactory
//     as the workdir.
//  2. A background goroutine waits on the contract via
//     AutonomousWait, then releases the worktree (force-remove +
//     drop branch on failure, plain release on success so the user
//     can review the branch).
//
// If no worktree.Manager is wired (single-agent mode), the runner
// falls through to bare SpawnFromFactory with whatever workdir the
// caller provides.
type parallelRunner struct {
	wt       *worktree.Manager
	sub      *subagent.Manager
	fallback string   // workdir to use when wt is nil
	released sync.Map // contractID → struct{}{}; idempotent release
}

func newParallelRunner(wt *worktree.Manager, sub *subagent.Manager, fallbackWorkdir string) *parallelRunner {
	return &parallelRunner{wt: wt, sub: sub, fallback: fallbackWorkdir}
}

// SpawnInWorktree allocates a worktree for the contract and spawns
// the subagent into it. Returns the contract ID on success; on
// spawn failure releases the just-allocated worktree so we don't
// leak a stale tree.
func (p *parallelRunner) SpawnInWorktree(ctx context.Context, c *subagent.Contract) (string, error) {
	if p == nil || p.sub == nil {
		return "", fmt.Errorf("parallel: subagent manager required")
	}
	if c == nil {
		return "", fmt.Errorf("parallel: contract required")
	}

	workdir := p.fallback
	var allocated *worktree.ManagedTree
	if p.wt != nil {
		mt, err := p.wt.Acquire(c.ID, worktree.AcquireOptions{
			LockReason: "subagent " + c.ID,
		})
		if err != nil {
			return "", fmt.Errorf("parallel: acquire worktree: %w", err)
		}
		allocated = mt
		workdir = mt.Path
	}

	id, err := p.sub.SpawnFromFactory(ctx, c, workdir)
	if err != nil {
		// Spawn failed → release the worktree we just took so we
		// don't leak a tree the subagent never touched.
		if allocated != nil {
			_ = p.wt.Release(c.ID, worktree.ReleaseOptions{Force: true})
		}
		return "", err
	}

	if allocated != nil {
		// Wait for completion in the background and release on
		// terminal state. We don't block the caller — they got
		// their contract ID and can poll Status / call Wait
		// themselves if they care.
		go p.waitAndRelease(ctx, c.ID)
	}
	return id, nil
}

// waitAndRelease blocks on AutonomousWait then releases the
// worktree. Force + DeleteBranch on failure paths because a
// half-finished tree isn't useful; plain release on success so the
// user can review the resulting branch + decide whether to merge.
func (p *parallelRunner) waitAndRelease(ctx context.Context, contractID string) {
	if _, dup := p.released.LoadOrStore(contractID, struct{}{}); dup {
		return
	}
	defer p.released.Delete(contractID)

	// Run AutonomousWait inside a recover so a panic in the sub-agent
	// goroutine doesn't skip the worktree release below.  Without this
	// a panicking sub-agent leaks a stale branch forever.
	var (
		report  *subagent.FinalReport
		waitErr error
	)
	func() {
		defer func() {
			if r := recover(); r != nil {
				waitErr = fmt.Errorf("panic in AutonomousWait: %v", r)
			}
		}()
		report, waitErr = p.sub.AutonomousWait(ctx, contractID)
	}()

	// Best-effort: any error path releases force+delete so we don't
	// leak stale branches when subagents crash.
	opts := worktree.ReleaseOptions{}
	if waitErr != nil || (report != nil && report.Status != "completed") {
		opts.Force = true
		opts.DeleteBranch = true
	}
	if rerr := p.wt.Release(contractID, opts); rerr != nil {
		log.Printf("parallel: release %s: %v", contractID, rerr)
	}
}

// resolveWorktreeManager builds a worktree.Manager rooted at the
// agent's cwd. Returns nil when not running inside a git repo (the
// parallel-worktree feature gracefully no-ops in non-git
// directories).
func resolveWorktreeManager(cwd string) *worktree.Manager {
	if cwd == "" {
		return nil
	}
	if _, err := os.Stat(cwd + "/.git"); err != nil {
		return nil
	}
	return worktree.NewManager(cwd, "")
}

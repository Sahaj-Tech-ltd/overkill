// Package lats — multi-branch task exploration (§8.5 Wave 4,
// inspired by Zhou 2024 LATS: Language Agent Tree Search).
//
// The idea: some tasks have multiple credible approaches and you
// don't know which will win until you try. Today the agent picks
// one path and grinds; if it dead-ends, a few turns are wasted
// before a retry from scratch. LATS instead spawns N branches in
// parallel — same task, different approaches — scores each, and
// keeps the winner.
//
// Honest scope:
//
//   - The paper runs full Monte Carlo Tree Search with rollouts,
//     backpropagated value estimates, and UCB exploration. We
//     ship a lightweight 2-N branch racer first; full MCTS
//     remains an open research target.
//   - Each branch is a separate agent run, gated to its own
//     worktree (via internal/worktree.Manager). Branches don't
//     trample each other's filesystem.
//   - Scoring is pluggable via the Scorer interface. A useful
//     default: "did the agent claim success without errors AND
//     did the tests pass?" The wiring layer composes its own
//     scorer (test runner, lint clean, etc.).
//
// What this package does NOT do: actually run an LLM. It
// orchestrates branches, collects their outcomes, ranks them.
// The caller supplies a Runner that knows how to drive one branch
// to completion.
package lats

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Branch is one candidate approach for the task. The Approach
// string is what the orchestrator passes to the Runner — it
// could be a prompt prefix ("solve this with iterative deepening"),
// an algorithm tag, or any free-form caller-supplied hint.
type Branch struct {
	ID       string
	Approach string
	// Tags is operator-supplied metadata. Surfaced in BranchResult
	// for the scorer + the eventual winner-pick line.
	Tags []string
}

// BranchResult captures what one branch produced.
type BranchResult struct {
	Branch    Branch
	Outcome   string        // "completed", "failed", "timeout", "cancelled"
	Response  string        // the final agent response (for the scorer + audit)
	StartedAt time.Time
	EndedAt   time.Time
	Duration  time.Duration
	Score     float64 // populated by the Scorer post-run
	Err       error
	// WorkdirPath is the per-branch worktree (or fallback cwd)
	// the Runner used. Used by the orchestrator to release the
	// worktree on cleanup.
	WorkdirPath string
}

// Runner drives one branch to completion. Implementations:
//
//   - A function that spawns a subagent with the branch's
//     approach as system-prompt prefix.
//   - A test stub that returns canned results.
//
// The contract: Run blocks until the branch finishes OR ctx is
// cancelled. Returns the outcome string + the agent's final
// response. workdir is the path the branch should treat as its
// working directory (caller-managed worktree).
type Runner interface {
	Run(ctx context.Context, branch Branch, workdir string) (outcome, response string, err error)
}

// RunnerFunc adapts a function to Runner.
type RunnerFunc func(ctx context.Context, branch Branch, workdir string) (string, string, error)

func (f RunnerFunc) Run(ctx context.Context, b Branch, w string) (string, string, error) {
	return f(ctx, b, w)
}

// Scorer evaluates a BranchResult and returns a comparable score.
// Higher is better. Score may inspect Outcome, Response, Duration,
// or run external checks (test runner, lint).
type Scorer interface {
	Score(result *BranchResult) float64
}

// ScorerFunc adapts a function to Scorer.
type ScorerFunc func(*BranchResult) float64

func (f ScorerFunc) Score(r *BranchResult) float64 { return f(r) }

// DefaultScorer is a simple baseline:
//
//   - completed-without-error: +1.0
//   - response non-empty:       +0.1
//   - error or timeout:         -0.5
//
// Real callers should compose richer scorers (test pass rate,
// lint clean, byte-cost penalty).
type DefaultScorer struct{}

func (DefaultScorer) Score(r *BranchResult) float64 {
	if r == nil {
		return 0
	}
	score := 0.0
	switch r.Outcome {
	case "completed":
		score += 1.0
	case "failed", "timeout":
		score -= 0.5
	case "cancelled":
		return 0 // neutral — caller cancelled, not the branch's fault
	}
	if strings.TrimSpace(r.Response) != "" {
		score += 0.1
	}
	if r.Err != nil {
		score -= 0.3
	}
	return score
}

// WorktreeProvider allocates + releases a worktree per branch.
// Optional — when nil, all branches share the caller-supplied
// fallback workdir. Wired to internal/worktree.Manager.Acquire/
// Release at the cmd layer.
type WorktreeProvider interface {
	Acquire(branchID string) (path string, release func(), err error)
}

// Options tunes orchestration.
type Options struct {
	// MaxBranches caps how many branches run in parallel. Above
	// this many candidates, the orchestrator truncates. Default 2.
	MaxBranches int
	// PerBranchTimeout bounds each branch's runtime. Default 10
	// minutes — branches typically finish faster.
	PerBranchTimeout time.Duration
	// FallbackWorkdir is the path used when WorktreeProvider is
	// nil. Typically the caller's cwd.
	FallbackWorkdir string
	// CancelLosersOnWin, when true, cancels still-running branches
	// once one completes with a positive score. Saves compute at
	// the cost of giving up the chance that a slower branch was
	// actually better. Default false — let all branches finish so
	// the scorer has full data.
	CancelLosersOnWin bool
}

func (o Options) maxBranches() int {
	if o.MaxBranches <= 0 {
		return 2
	}
	return o.MaxBranches
}

func (o Options) perBranchTimeout() time.Duration {
	if o.PerBranchTimeout <= 0 {
		return 10 * time.Minute
	}
	return o.PerBranchTimeout
}

// Race runs every branch in parallel through the Runner, scores
// each via the Scorer, and returns the results sorted highest-
// score-first. Caller picks results[0] as the winner.
//
// Errors: if NO branch produced a usable result, returns the first
// branch's error. Otherwise returns nil even when individual
// branches failed — partial success is the common case for tree
// search and the scorer handles failure penalties.
func Race(
	ctx context.Context,
	branches []Branch,
	runner Runner,
	scorer Scorer,
	opts Options,
	wt WorktreeProvider,
) ([]*BranchResult, error) {
	if runner == nil {
		return nil, errors.New("lats: runner required")
	}
	if scorer == nil {
		scorer = DefaultScorer{}
	}
	if len(branches) == 0 {
		return nil, errors.New("lats: no branches supplied")
	}
	max := opts.maxBranches()
	if len(branches) > max {
		branches = branches[:max]
	}
	timeout := opts.perBranchTimeout()

	// Allocate IDs for any branch missing one.
	for i := range branches {
		if branches[i].ID == "" {
			branches[i].ID = uuid.NewString()
		}
	}

	results := make([]*BranchResult, len(branches))
	var wg sync.WaitGroup
	wg.Add(len(branches))

	// One cancellable context per branch so CancelLosersOnWin can
	// surgically stop the others.
	branchCtxs := make([]context.Context, len(branches))
	branchCancels := make([]context.CancelFunc, len(branches))
	for i := range branches {
		branchCtxs[i], branchCancels[i] = context.WithTimeout(ctx, timeout)
	}
	defer func() {
		for _, c := range branchCancels {
			c()
		}
	}()

	// Winner-cancel channel: closed when the first branch lands a
	// positive-score completion. Only used when
	// CancelLosersOnWin is true.
	winnerSignal := make(chan struct{})
	var winnerOnce sync.Once
	signalWin := func() {
		if !opts.CancelLosersOnWin {
			return
		}
		winnerOnce.Do(func() { close(winnerSignal) })
	}

	for i := range branches {
		i := i
		go func() {
			defer wg.Done()
			b := branches[i]
			res := &BranchResult{
				Branch:    b,
				StartedAt: time.Now().UTC(),
			}

			// Worktree allocation.
			workdir := opts.FallbackWorkdir
			var release func()
			if wt != nil {
				path, rel, err := wt.Acquire(b.ID)
				if err != nil {
					res.Err = fmt.Errorf("lats: acquire worktree: %w", err)
					res.Outcome = "failed"
					res.EndedAt = time.Now().UTC()
					res.Duration = res.EndedAt.Sub(res.StartedAt)
					results[i] = res
					return
				}
				workdir = path
				release = rel
			}
			res.WorkdirPath = workdir
			if release != nil {
				defer release()
			}

			outcome, response, runErr := runner.Run(branchCtxs[i], b, workdir)
			res.EndedAt = time.Now().UTC()
			res.Duration = res.EndedAt.Sub(res.StartedAt)
			res.Outcome = outcome
			res.Response = response
			res.Err = runErr
			if branchCtxs[i].Err() == context.Canceled {
				res.Outcome = "cancelled"
			}
			res.Score = scorer.Score(res)
			results[i] = res

			// If we won and the option is enabled, cancel the
			// others. We define "won" as positive score on a
			// completed outcome — losing branches still surface
			// for the scorer to inspect, just truncated.
			if opts.CancelLosersOnWin && res.Outcome == "completed" && res.Score > 0 {
				signalWin()
			}
		}()
	}

	// Goroutine to cancel losers on the win signal.
	if opts.CancelLosersOnWin {
		go func() {
			select {
			case <-winnerSignal:
				for _, c := range branchCancels {
					c()
				}
			case <-ctx.Done():
			}
		}()
	}

	wg.Wait()

	// Filter nils (shouldn't happen, but defensive).
	out := results[:0]
	for _, r := range results {
		if r != nil {
			out = append(out, r)
		}
	}
	// Sort highest-score-first.
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Score > out[j].Score
	})

	// If every branch failed AND nothing returned a response,
	// surface the first error so the caller can distinguish "no
	// winner" from "ran but scored zero".
	if len(out) == 0 {
		return nil, errors.New("lats: no branches produced results")
	}
	usable := false
	for _, r := range out {
		if r.Outcome == "completed" || strings.TrimSpace(r.Response) != "" {
			usable = true
			break
		}
	}
	if !usable {
		return out, fmt.Errorf("lats: all %d branches failed; first err: %w", len(out), out[0].Err)
	}
	return out, nil
}

// FormatWinnerSummary renders a one-line summary suitable for
// surfacing to the user: "winner: <approach> (score 1.10, 2.3s);
// losers: <approach2> (0.10, 4.1s)…". Empty when no results.
func FormatWinnerSummary(results []*BranchResult) string {
	if len(results) == 0 {
		return ""
	}
	var b strings.Builder
	w := results[0]
	fmt.Fprintf(&b, "winner: %s (score %.2f, %s)",
		w.Branch.Approach, w.Score, w.Duration.Round(time.Millisecond))
	if len(results) > 1 {
		b.WriteString("; losers: ")
		for i, r := range results[1:] {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "%s (%.2f, %s)",
				r.Branch.Approach, r.Score, r.Duration.Round(time.Millisecond))
		}
	}
	return b.String()
}

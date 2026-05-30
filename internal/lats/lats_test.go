package lats

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRace_SortsHighestScoreFirst(t *testing.T) {
	branches := []Branch{
		{Approach: "slow"},
		{Approach: "fast"},
	}
	runner := RunnerFunc(func(_ context.Context, b Branch, _ string) (string, string, error) {
		if b.Approach == "fast" {
			return "completed", "fast response", nil
		}
		return "completed", "slow response", nil
	})
	scorer := ScorerFunc(func(r *BranchResult) float64 {
		if r.Branch.Approach == "fast" {
			return 1.0
		}
		return 0.5
	})
	results, err := Race(context.Background(), branches, runner, scorer, Options{MaxBranches: 5}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Branch.Approach != "fast" {
		t.Errorf("highest-score should be first: %s", results[0].Branch.Approach)
	}
}

func TestRace_DefaultScorerCompletedBeatsFailed(t *testing.T) {
	branches := []Branch{
		{Approach: "wins"},
		{Approach: "loses"},
	}
	runner := RunnerFunc(func(_ context.Context, b Branch, _ string) (string, string, error) {
		if b.Approach == "wins" {
			return "completed", "ok", nil
		}
		return "failed", "", errors.New("nope")
	})
	results, _ := Race(context.Background(), branches, runner, nil, Options{}, nil)
	if results[0].Branch.Approach != "wins" {
		t.Errorf("completed should beat failed under default scorer")
	}
}

func TestRace_TruncatesToMaxBranches(t *testing.T) {
	branches := []Branch{
		{Approach: "a"}, {Approach: "b"}, {Approach: "c"}, {Approach: "d"},
	}
	runner := RunnerFunc(func(_ context.Context, _ Branch, _ string) (string, string, error) {
		return "completed", "ok", nil
	})
	results, _ := Race(context.Background(), branches, runner, nil, Options{MaxBranches: 2}, nil)
	if len(results) != 2 {
		t.Errorf("expected 2 results (MaxBranches=2), got %d", len(results))
	}
}

func TestRace_PerBranchTimeoutFires(t *testing.T) {
	branches := []Branch{{Approach: "slow"}}
	runner := RunnerFunc(func(ctx context.Context, _ Branch, _ string) (string, string, error) {
		select {
		case <-time.After(5 * time.Second):
			return "completed", "should not reach", nil
		case <-ctx.Done():
			return "timeout", "", ctx.Err()
		}
	})
	start := time.Now()
	results, _ := Race(context.Background(), branches, runner, nil, Options{PerBranchTimeout: 50 * time.Millisecond}, nil)
	if time.Since(start) > time.Second {
		t.Errorf("timeout should fire quickly, took %s", time.Since(start))
	}
	if len(results) != 1 || results[0].Outcome == "completed" {
		t.Errorf("expected timeout outcome, got %+v", results[0])
	}
}

func TestRace_CancelLosersOnWin(t *testing.T) {
	var slowRan atomic.Bool
	branches := []Branch{
		{Approach: "fast"},
		{Approach: "slow"},
	}
	runner := RunnerFunc(func(ctx context.Context, b Branch, _ string) (string, string, error) {
		if b.Approach == "fast" {
			return "completed", "win", nil
		}
		// Slow branch — wait long enough that cancel should fire.
		select {
		case <-time.After(2 * time.Second):
			slowRan.Store(true)
			return "completed", "late win", nil
		case <-ctx.Done():
			return "cancelled", "", ctx.Err()
		}
	})
	scorer := ScorerFunc(func(r *BranchResult) float64 {
		if r.Outcome == "completed" {
			return 1.0
		}
		return 0
	})
	results, _ := Race(context.Background(), branches, runner, scorer,
		Options{CancelLosersOnWin: true, PerBranchTimeout: 5 * time.Second}, nil)
	if slowRan.Load() {
		t.Error("slow branch should be cancelled before completion")
	}
	if results[0].Branch.Approach != "fast" {
		t.Errorf("fast branch should win")
	}
}

func TestRace_AcquiresWorktreePerBranch(t *testing.T) {
	branches := []Branch{
		{Approach: "a"},
		{Approach: "b"},
	}
	var acquired []string
	var mu = &dummyMutex{}
	wt := &fakeWTProvider{
		acquireFn: func(id string) (string, func(), error) {
			mu.Lock()
			acquired = append(acquired, id)
			mu.Unlock()
			return "/tmp/wt/" + id, func() {}, nil
		},
	}
	runner := RunnerFunc(func(_ context.Context, b Branch, workdir string) (string, string, error) {
		if !strings.HasPrefix(workdir, "/tmp/wt/") {
			return "failed", "", fmt.Errorf("expected wt workdir, got %s", workdir)
		}
		return "completed", "ok", nil
	})
	_, err := Race(context.Background(), branches, runner, nil, Options{MaxBranches: 5}, wt)
	if err != nil {
		t.Fatal(err)
	}
	if len(acquired) != 2 {
		t.Errorf("expected 2 worktree acquires, got %d", len(acquired))
	}
}

func TestRace_NoBranchesIsError(t *testing.T) {
	runner := RunnerFunc(func(_ context.Context, _ Branch, _ string) (string, string, error) {
		return "", "", nil
	})
	if _, err := Race(context.Background(), nil, runner, nil, Options{}, nil); err == nil {
		t.Error("empty branches should error")
	}
}

func TestRace_NilRunnerIsError(t *testing.T) {
	branches := []Branch{{Approach: "a"}}
	if _, err := Race(context.Background(), branches, nil, nil, Options{}, nil); err == nil {
		t.Error("nil runner should error")
	}
}

func TestRace_AllBranchesFailedSurfaceError(t *testing.T) {
	branches := []Branch{{Approach: "a"}, {Approach: "b"}}
	runner := RunnerFunc(func(_ context.Context, _ Branch, _ string) (string, string, error) {
		return "failed", "", errors.New("nope")
	})
	_, err := Race(context.Background(), branches, runner, nil, Options{}, nil)
	if err == nil {
		t.Error("all-failed should surface error")
	}
}

func TestFormatWinnerSummary_RendersWinnerAndLosers(t *testing.T) {
	results := []*BranchResult{
		{Branch: Branch{Approach: "fast"}, Score: 1.1, Duration: 2 * time.Second},
		{Branch: Branch{Approach: "slow"}, Score: 0.1, Duration: 4 * time.Second},
	}
	got := FormatWinnerSummary(results)
	if !strings.Contains(got, "winner: fast") {
		t.Errorf("missing winner: %s", got)
	}
	if !strings.Contains(got, "losers:") || !strings.Contains(got, "slow") {
		t.Errorf("missing losers: %s", got)
	}
}

func TestDefaultScorer_PenalizesErrors(t *testing.T) {
	winner := DefaultScorer{}.Score(&BranchResult{Outcome: "completed", Response: "ok"})
	withErr := DefaultScorer{}.Score(&BranchResult{Outcome: "completed", Response: "ok", Err: errors.New("warn")})
	if withErr >= winner {
		t.Errorf("err penalty not applied: clean=%v with_err=%v", winner, withErr)
	}
}

// ── test helpers ────────────────────────────────────────────────────

type fakeWTProvider struct {
	acquireFn func(string) (string, func(), error)
}

func (f *fakeWTProvider) Acquire(id string) (string, func(), error) {
	return f.acquireFn(id)
}

type dummyMutex struct {
	mu    sync.Mutex
	count atomic.Int32
}

func (d *dummyMutex) Lock()   { d.mu.Lock(); d.count.Add(1) }
func (d *dummyMutex) Unlock() { d.mu.Unlock() }

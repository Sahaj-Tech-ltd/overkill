package doctor

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestRunner_RunsAllChecks(t *testing.T) {
	r := NewRunner()
	r.PerCheckTimeout = 100 * time.Millisecond

	calls := map[string]bool{}
	r.Register(SubsystemCheck{ID: "a", Name: "A", Category: CatCore, Fn: func(ctx context.Context) Result {
		calls["a"] = true
		return Result{Status: SevOK, Detail: "ok"}
	}})
	r.Register(SubsystemCheck{ID: "b", Name: "B", Category: CatCore, Parallel: true, Fn: func(ctx context.Context) Result {
		calls["b"] = true
		return Result{Status: SevWarn, Detail: "warn", Fix: "fix me"}
	}})

	s := r.Run(context.Background())

	if !calls["a"] || !calls["b"] {
		t.Fatalf("expected both checks to run, got %v", calls)
	}
	if s.Counts.OK != 1 || s.Counts.Warn != 1 {
		t.Fatalf("counts wrong: %+v", s.Counts)
	}
	if len(s.Checks) != 2 {
		t.Fatalf("expected 2 results, got %d", len(s.Checks))
	}
}

func TestRunner_PanicsAreContained(t *testing.T) {
	r := NewRunner()
	r.PerCheckTimeout = 100 * time.Millisecond
	r.Register(SubsystemCheck{ID: "boom", Name: "boom", Fn: func(ctx context.Context) Result {
		panic("nope")
	}})
	r.Register(SubsystemCheck{ID: "ok", Name: "ok", Fn: func(ctx context.Context) Result {
		return Result{Status: SevOK}
	}})
	s := r.Run(context.Background())
	if s.Counts.Fail != 1 || s.Counts.OK != 1 {
		t.Fatalf("expected one fail and one ok, got %+v", s.Counts)
	}
}

func TestRunner_RespectsOverallTimeout(t *testing.T) {
	r := NewRunner()
	r.OverallTimeout = 50 * time.Millisecond
	r.PerCheckTimeout = 200 * time.Millisecond
	r.Register(SubsystemCheck{ID: "slow", Name: "slow", Fn: func(ctx context.Context) Result {
		select {
		case <-ctx.Done():
			return Result{Status: SevFail, Detail: "ctx done"}
		case <-time.After(500 * time.Millisecond):
			return Result{Status: SevOK}
		}
	}})
	start := time.Now()
	s := r.Run(context.Background())
	if time.Since(start) > 300*time.Millisecond {
		t.Fatalf("runner did not honor overall timeout")
	}
	if s.Counts.Fail != 1 {
		t.Fatalf("expected fail when context fired, got %+v", s.Counts)
	}
}

func TestSummary_GroupsByCategory(t *testing.T) {
	r := NewRunner()
	r.Register(SubsystemCheck{ID: "x", Name: "x", Category: CatSystem, Fn: func(ctx context.Context) Result { return Result{Status: SevOK} }})
	r.Register(SubsystemCheck{ID: "y", Name: "y", Category: CatCore, Fn: func(ctx context.Context) Result { return Result{Status: SevOK} }})
	s := r.Run(context.Background())
	if s.Checks[0].Category != CatCore {
		t.Fatalf("expected Core first, got %s", s.Checks[0].Category)
	}
}

func TestPrettyPrint_RendersCategories(t *testing.T) {
	r := NewRunner()
	r.Register(SubsystemCheck{ID: "x", Name: "Core check", Category: CatCore, Fn: func(ctx context.Context) Result { return Result{Status: SevOK, Detail: "fine"} }})
	r.Register(SubsystemCheck{ID: "y", Name: "Bad check", Category: CatProvider, Fn: func(ctx context.Context) Result {
		return Result{Status: SevFail, Detail: "broken", Fix: "do something"}
	}})
	s := r.Run(context.Background())

	var sb strings.Builder
	PrettyPrint(&sb, s, PrettyOptions{NoColor: true})
	out := sb.String()
	for _, want := range []string{"Core", "Providers", "Core check", "Bad check", "fix:", "do something"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected output to contain %q, got:\n%s", want, out)
		}
	}
}

func TestJSON_EncodesCounts(t *testing.T) {
	r := NewRunner()
	r.Register(SubsystemCheck{ID: "x", Name: "x", Category: CatCore, Fn: func(ctx context.Context) Result { return Result{Status: SevOK} }})
	s := r.Run(context.Background())
	b, err := JSON(s)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"ok": 1`) {
		t.Fatalf("expected ok count in JSON, got %s", b)
	}
}

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/compaction"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tokenizer"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
)

type goalTestProvider struct {
	complete func(context.Context, providers.Request) (providers.Response, error)
	stream   func(context.Context, providers.Request) (<-chan providers.Chunk, error)
	calls    atomic.Int32
}

func (p *goalTestProvider) Name() string              { return "goal-test" }
func (p *goalTestProvider) Models() []providers.Model { return nil }
func (p *goalTestProvider) Complete(ctx context.Context, r providers.Request) (providers.Response, error) {
	p.calls.Add(1)
	if p.complete == nil {
		return providers.Response{}, fmt.Errorf("unexpected complete")
	}
	return p.complete(ctx, r)
}
func (p *goalTestProvider) Stream(ctx context.Context, r providers.Request) (<-chan providers.Chunk, error) {
	p.calls.Add(1)
	if p.stream == nil {
		return nil, fmt.Errorf("unexpected stream")
	}
	return p.stream(ctx, r)
}
func goalAgent(p providers.Provider, gs *GoalStore, sid string) *Agent {
	a := New(Config{Provider: p, Model: "goal-test", SessionID: sid, MaxSteps: 5, Tools: tools.NewRegistry()})
	a.SetGoalStore(gs)
	return a
}
func goalStream(ctx context.Context, a *Agent) (*RunResult, error) {
	ch, err := a.Stream(ctx, "work")
	if err != nil {
		return nil, err
	}
	var result *RunResult
	for e := range ch {
		if e.Type == EventError {
			err = e.Error
		}
		if e.Result != nil {
			result = e.Result
		}
	}
	return result, err
}
func goalChunks(chunks ...providers.Chunk) func(context.Context, providers.Request) (<-chan providers.Chunk, error) {
	return func(context.Context, providers.Request) (<-chan providers.Chunk, error) {
		ch := make(chan providers.Chunk, len(chunks))
		for _, c := range chunks {
			ch <- c
		}
		close(ch)
		return ch, nil
	}
}
func requireGoal(t *testing.T, gs *GoalStore, sid string) *Goal {
	t.Helper()
	g, e := gs.Get(context.Background(), sid)
	if e != nil || g == nil {
		t.Fatalf("get goal: %v %#v", e, g)
	}
	return g
}
func newExecutionGoalStore(t *testing.T) *GoalStore {
	t.Helper()
	gs, e := NewGoalStore(openGoalDB(t))
	if e != nil {
		t.Fatal(e)
	}
	return gs
}

func TestGoalExecution_ActualUsageAcrossRunStreamAndResume(t *testing.T) {
	ctx := context.Background()
	gs := newExecutionGoalStore(t)
	sid := "usage-resume"
	if err := gs.SetWithBudgets(ctx, sid, "ship", 41, 0); err != nil {
		t.Fatal(err)
	}
	p := &goalTestProvider{complete: func(context.Context, providers.Request) (providers.Response, error) {
		return providers.Response{Content: "done", Usage: providers.Usage{InputTokens: 8, OutputTokens: 3, CachedInputTokens: 4}}, nil
	}, stream: goalChunks(providers.Chunk{Content: "done", Usage: &providers.Usage{InputTokens: 10, OutputTokens: 3}, Done: true})}
	a := goalAgent(p, gs, sid)
	if _, e := a.Run(ctx, "work"); e != nil {
		t.Fatal(e)
	}
	if _, e := goalStream(ctx, a); e != nil {
		t.Fatal(e)
	}
	// Simulate a resumed process by constructing both a fresh store and agent.
	resumed, err := NewGoalStore(gs.db)
	if err != nil {
		t.Fatal(err)
	}
	q := &goalTestProvider{complete: func(context.Context, providers.Request) (providers.Response, error) {
		return providers.Response{Content: "done", Usage: providers.Usage{InputTokens: 12, OutputTokens: 5}}, nil
	}}
	b := goalAgent(q, resumed, sid)
	if _, e := b.Run(ctx, "continue"); e != nil {
		t.Fatal(e)
	}
	g := requireGoal(t, gs, sid)
	if g.TokensUsed != 41 || g.Status != "budget_limited" {
		t.Fatalf("persisted goal = %#v", g)
	}
	if _, e := b.Run(ctx, "over budget"); !errors.Is(e, ErrGoalBudget) {
		t.Fatalf("budget admission: %v", e)
	}
	if e := gs.Pause(ctx, sid); e != nil {
		t.Fatal(e)
	}
	if e := gs.Resume(ctx, sid); e != nil {
		t.Fatal(e)
	}
	if _, e := goalStream(ctx, b); !errors.Is(e, ErrGoalBudget) {
		t.Fatalf("resumed exhausted budget: %v", e)
	}
	if p.calls.Load() != 2 || q.calls.Load() != 1 {
		t.Fatalf("unexpected calls %d %d", p.calls.Load(), q.calls.Load())
	}
	var ledgerTokens int
	if e := gs.db.QueryRow(`SELECT COALESCE(SUM(tokens_used),0) FROM goal_turn_usage WHERE goal_id=$1`, g.ID).Scan(&ledgerTokens); e != nil || ledgerTokens != 41 {
		t.Fatalf("ledger tokens %d: %v", ledgerTokens, e)
	}
}

func TestGoalExecution_BudgetStopsToolsAndNextProvider(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint("stream=", stream), func(t *testing.T) {
			gs := newExecutionGoalStore(t)
			ctx := context.Background()
			sid := "stop-tools"
			if e := gs.SetWithBudgets(ctx, sid, "ship", 5, 0); e != nil {
				t.Fatal(e)
			}
			call := providers.ToolCall{ID: "call", Name: "work", Arguments: "{}"}
			usage := providers.Usage{InputTokens: 3, OutputTokens: 2}
			p := &goalTestProvider{complete: func(context.Context, providers.Request) (providers.Response, error) {
				return providers.Response{ToolCalls: []providers.ToolCall{call}, Usage: usage}, nil
			}, stream: goalChunks(providers.Chunk{ToolCalls: []providers.ToolCall{call}, Usage: &usage, Done: true})}
			a := goalAgent(p, gs, sid)
			var toolRuns atomic.Int32
			a.toolRegistry.Register(&mockTool{name: "work", execute: func(context.Context, json.RawMessage) (json.RawMessage, error) {
				toolRuns.Add(1)
				return json.RawMessage(`{}`), nil
			}})
			var err error
			if stream {
				_, err = goalStream(ctx, a)
			} else {
				_, err = a.Run(ctx, "work")
			}
			if !errors.Is(err, ErrGoalBudget) {
				t.Fatalf("expected budget error, got %v", err)
			}
			if toolRuns.Load() != 0 || p.calls.Load() != 1 {
				t.Fatalf("over-budget work: tool=%d provider=%d", toolRuns.Load(), p.calls.Load())
			}
			if g := requireGoal(t, gs, sid); g.TokensUsed != 5 || g.Status != "budget_limited" {
				t.Fatalf("goal %#v", g)
			}
		})
	}
}

func TestGoalExecution_StreamSnapshotsPartialAndFailure(t *testing.T) {
	gs := newExecutionGoalStore(t)
	ctx := context.Background()
	sid := "snapshot"
	if e := gs.Set(ctx, sid, "ship"); e != nil {
		t.Fatal(e)
	}
	boom := errors.New("transport failed")
	p := &goalTestProvider{stream: goalChunks(
		providers.Chunk{Content: "a", Usage: &providers.Usage{InputTokens: 10, CachedInputTokens: 5}},
		providers.Chunk{Usage: &providers.Usage{InputTokens: 10, OutputTokens: 2}},
		providers.Chunk{Usage: &providers.Usage{OutputTokens: 3}},
		providers.Chunk{Usage: &providers.Usage{InputTokens: 10, OutputTokens: 3}},
		providers.Chunk{Err: boom, Usage: &providers.Usage{OutputTokens: 4}},
	)}
	if _, e := goalStream(ctx, goalAgent(p, gs, sid)); !errors.Is(e, boom) {
		t.Fatalf("stream error: %v", e)
	}
	if g := requireGoal(t, gs, sid); g.TokensUsed != 14 {
		t.Fatalf("split/repeated usage = %d, want 14", g.TokensUsed)
	}
	missing := &goalTestProvider{stream: goalChunks(providers.Chunk{Content: "content has no usage", Done: true})}
	if _, e := goalStream(ctx, goalAgent(missing, gs, sid)); e != nil {
		t.Fatal(e)
	}
	if g := requireGoal(t, gs, sid); g.TokensUsed != 14 {
		t.Fatalf("fabricated omitted usage: %d", g.TokensUsed)
	}
}

func TestGoalExecution_CancelPersistsReportedUsageAndFractions(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint("stream=", stream), func(t *testing.T) {
			gs := newExecutionGoalStore(t)
			ctx := context.Background()
			sid := "cancel"
			if e := gs.Set(ctx, sid, "ship"); e != nil {
				t.Fatal(e)
			}
			started := make(chan struct{})
			cancelCtx, cancel := context.WithCancel(ctx)
			p := &goalTestProvider{complete: func(ctx context.Context, _ providers.Request) (providers.Response, error) {
				close(started)
				<-ctx.Done()
				return providers.Response{Usage: providers.Usage{InputTokens: 7}}, ctx.Err()
			}, stream: func(ctx context.Context, _ providers.Request) (<-chan providers.Chunk, error) {
				ch := make(chan providers.Chunk, 1)
				ch <- providers.Chunk{Usage: &providers.Usage{InputTokens: 7}}
				go func() { time.Sleep(25 * time.Millisecond); close(started); <-ctx.Done(); close(ch) }()
				return ch, nil
			}}
			done := make(chan error, 1)
			go func() {
				a := goalAgent(p, gs, sid)
				var e error
				if stream {
					_, e = goalStream(cancelCtx, a)
				} else {
					_, e = a.Run(cancelCtx, "work")
				}
				done <- e
			}()
			<-started
			time.Sleep(30 * time.Millisecond)
			cancel()
			if e := <-done; !errors.Is(e, context.Canceled) {
				t.Fatalf("cancel error: %v", e)
			}
			g := requireGoal(t, gs, sid)
			if g.TokensUsed != 7 || g.TimeUsedNanoseconds < int64(25*time.Millisecond) || g.TimeUsedSeconds != 0 {
				t.Fatalf("cancellation usage: %#v", g)
			}
			// A new turn contributes a fraction rather than rounding each turn to zero.
			q := &goalTestProvider{complete: func(context.Context, providers.Request) (providers.Response, error) {
				time.Sleep(30 * time.Millisecond)
				return providers.Response{Content: "done"}, nil
			}}
			if _, e := goalAgent(q, gs, sid).Run(ctx, "resume"); e != nil {
				t.Fatal(e)
			}
			after := requireGoal(t, gs, sid)
			if after.TimeUsedNanoseconds <= g.TimeUsedNanoseconds+int64(25*time.Millisecond) {
				t.Fatalf("lost time fraction: before=%d after=%d", g.TimeUsedNanoseconds, after.TimeUsedNanoseconds)
			}
		})
	}
}

func TestGoalExecution_TimeDeadlineInterruptsProviderAndTool(t *testing.T) {
	for _, mode := range []string{"run", "stream", "tool"} {
		t.Run(mode, func(t *testing.T) {
			gs := newExecutionGoalStore(t)
			ctx := context.Background()
			sid := "time-limit"
			if e := gs.SetWithBudgets(ctx, sid, "ship", 0, 1); e != nil {
				t.Fatal(e)
			}
			// Leave only 60 ms of the persisted budget for the next turn.
			if _, e := gs.db.Exec(`UPDATE goals SET time_used_nanoseconds=940000000 WHERE session_id=$1`, sid); e != nil {
				t.Fatal(e)
			}
			p := &goalTestProvider{complete: func(ctx context.Context, _ providers.Request) (providers.Response, error) {
				if mode == "tool" {
					return providers.Response{ToolCalls: []providers.ToolCall{{ID: "call", Name: "wait", Arguments: "{}"}}, Usage: providers.Usage{InputTokens: 2}}, nil
				}
				<-ctx.Done()
				return providers.Response{}, ctx.Err()
			}, stream: func(ctx context.Context, _ providers.Request) (<-chan providers.Chunk, error) {
				ch := make(chan providers.Chunk)
				go func() { <-ctx.Done(); close(ch) }()
				return ch, nil
			}}
			a := goalAgent(p, gs, sid)
			var toolRuns atomic.Int32
			a.toolRegistry.Register(&mockTool{name: "wait", execute: func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
				toolRuns.Add(1)
				<-ctx.Done()
				return nil, ctx.Err()
			}})
			start := time.Now()
			var err error
			if mode == "stream" {
				_, err = goalStream(ctx, a)
			} else {
				_, err = a.Run(ctx, "work")
			}
			if err == nil || time.Since(start) > 2*time.Second {
				t.Fatalf("deadline not enforced: %v after %v", err, time.Since(start))
			}
			g := requireGoal(t, gs, sid)
			if g.Status != "budget_limited" || g.TimeUsedNanoseconds < int64(time.Second) {
				t.Fatalf("time not persisted/enforced: %#v", g)
			}
			if mode == "tool" && toolRuns.Load() != 1 {
				t.Fatalf("tool never reached deadline: %d", toolRuns.Load())
			}
			if _, e := goalAgent(p, gs, sid).Run(ctx, "resume"); !errors.Is(e, ErrGoalBudget) {
				t.Fatalf("time budget resume: %v", e)
			}
		})
	}
}

func TestGoalExecution_ConcurrentAdmissionAndReplacementAttribution(t *testing.T) {
	gs := newExecutionGoalStore(t)
	ctx := context.Background()
	sid := "concurrent"
	if e := gs.SetWithBudgets(ctx, sid, "original", 20, 0); e != nil {
		t.Fatal(e)
	}
	old := requireGoal(t, gs, sid)
	started := make(chan struct{})
	release := make(chan struct{})
	p := &goalTestProvider{complete: func(context.Context, providers.Request) (providers.Response, error) {
		close(started)
		<-release
		return providers.Response{Content: "done", Usage: providers.Usage{InputTokens: 9}}, nil
	}}
	first := goalAgent(p, gs, sid)
	second := goalAgent(p, gs, sid)
	done := make(chan error, 1)
	go func() { _, e := first.Run(ctx, "work"); done <- e }()
	<-started
	if _, e := second.Run(ctx, "concurrent"); !errors.Is(e, ErrGoalTurnActive) {
		t.Fatalf("concurrent admission: %v", e)
	}
	if e := gs.SetWithBudgets(ctx, sid, "replacement", 30, 0); e != nil {
		t.Fatal(e)
	}
	replacement := requireGoal(t, gs, sid)
	if replacement.ID == old.ID {
		t.Fatal("replacement did not receive a new goal ID")
	}
	close(release)
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	g := requireGoal(t, gs, sid)
	if g.TokensUsed != 0 || g.TimeUsedNanoseconds != 0 {
		t.Fatalf("old turn charged replacement: %#v", g)
	}
	var used int
	var ended bool
	if e := gs.db.QueryRow(`SELECT tokens_used,ended_at IS NOT NULL FROM goal_turn_usage WHERE goal_id=$1`, old.ID).Scan(&used, &ended); e != nil || used != 9 || !ended {
		t.Fatalf("old goal ledger usage=%d ended=%v err=%v", used, ended, e)
	}
	q := &goalTestProvider{complete: func(context.Context, providers.Request) (providers.Response, error) {
		return providers.Response{Content: "done", Usage: providers.Usage{InputTokens: 3}}, nil
	}}
	if _, e := goalAgent(q, gs, sid).Run(ctx, "replacement work"); e != nil {
		t.Fatalf("lease not released: %v", e)
	}
	if got := requireGoal(t, gs, sid).TokensUsed; got != 3 {
		t.Fatalf("replacement usage %d", got)
	}
}

func TestGoalExecution_AuxiliaryModelCallsUseActualUsage(t *testing.T) {
	for _, mode := range []string{"compact", "lcm", "title", "variants", "prompt-compress"} {
		t.Run(mode, func(t *testing.T) {
			gs := newExecutionGoalStore(t)
			ctx := context.Background()
			sid := "aux"
			if e := gs.Set(ctx, sid, "ship"); e != nil {
				t.Fatal(e)
			}
			p := &goalTestProvider{complete: func(context.Context, providers.Request) (providers.Response, error) {
				return providers.Response{Content: "summary", Usage: providers.Usage{InputTokens: 5, OutputTokens: 2}}, nil
			}}
			a := goalAgent(p, gs, sid)
			a.RestoreHistory([]providers.Message{{Role: "user", Content: "first"}, {Role: "assistant", Content: "second"}, {Role: "user", Content: "third"}})
			switch mode {
			case "compact":
				if _, e := a.Compact(ctx); e != nil {
					t.Fatal(e)
				}
			case "lcm":
				c := compaction.NewAgentCompactor(p, tokenizer.NewEstimator(), 1)
				c.SetCompactionModel("goal-test")
				a.SetCompactor(c, true)
				if _, e := a.Compact(ctx); e != nil {
					t.Fatal(e)
				}
			case "title":
				if _, e := a.GenerateTitle(ctx); e != nil {
					t.Fatal(e)
				}
			case "variants":
				if _, e := a.RunVariants(ctx, "compare", []string{"one", "two"}); e != nil {
					t.Fatal(e)
				}
			case "prompt-compress":
				scoped, state, e := a.beginGoalTurn(ctx)
				if e != nil {
					t.Fatal(e)
				}
				_, _, e = compaction.NewPromptCompressor(p, "goal-test").Compress(scoped, strings.Repeat("lengthy prompt ", 200))
				if e != nil {
					t.Fatal(e)
				}
				if e := state.RecordTurnEnd(scoped, 0); e != nil {
					t.Fatal(e)
				}
			}
			calls := int(p.calls.Load())
			if calls < 1 {
				t.Fatal("model was not exercised")
			}
			if got := requireGoal(t, gs, sid).TokensUsed; got != 7*calls {
				t.Fatalf("usage %d want %d for %d calls", got, 7*calls, calls)
			}
		})
	}
}

func TestGoalExecution_AdmissionReusesSingleConnectionForGoalTools(t *testing.T) {
	gs := newExecutionGoalStore(t)
	gs.db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	sid := "one-connection"
	if e := gs.Set(ctx, sid, "ship"); e != nil {
		t.Fatal(e)
	}
	var calls atomic.Int32
	p := &goalTestProvider{complete: func(context.Context, providers.Request) (providers.Response, error) {
		if calls.Add(1) == 1 {
			return providers.Response{ToolCalls: []providers.ToolCall{{ID: "goal-read", Name: "goal-read", Arguments: "{}"}}, Usage: providers.Usage{InputTokens: 1}}, nil
		}
		return providers.Response{Content: "done", Usage: providers.Usage{InputTokens: 2}}, nil
	}}
	a := goalAgent(p, gs, sid)
	a.toolRegistry.Register(&mockTool{name: "goal-read", execute: func(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
		if e := gs.SetStatus(ctx, sid, "active"); e != nil {
			return nil, e
		}
		return gs.GetJSON(ctx, sid)
	}})
	if _, e := a.Run(ctx, "work"); e != nil {
		t.Fatalf("single-connection turn: %v", e)
	}
	if got := requireGoal(t, gs, sid).TokensUsed; got != 3 {
		t.Fatalf("usage %d", got)
	}
}

func TestGoalExecution_CancelFullStreamBufferReleasesAdmission(t *testing.T) {
	gs := newExecutionGoalStore(t)
	ctx := context.Background()
	sid := "full-buffer"
	if e := gs.Set(ctx, sid, "ship"); e != nil {
		t.Fatal(e)
	}
	produced := make(chan struct{})
	p := &goalTestProvider{stream: func(context.Context, providers.Request) (<-chan providers.Chunk, error) {
		ch := make(chan providers.Chunk, 200)
		ch <- providers.Chunk{Usage: &providers.Usage{InputTokens: 4}}
		for i := 0; i < 150; i++ {
			ch <- providers.Chunk{Content: "x"}
		}
		close(ch)
		close(produced)
		return ch, nil
	}}
	a := goalAgent(p, gs, sid)
	runCtx, cancel := context.WithCancel(ctx)
	events, e := a.Stream(runCtx, "work")
	if e != nil {
		t.Fatal(e)
	}
	<-produced
	time.Sleep(20 * time.Millisecond)
	cancel()
	deadline := time.After(2 * time.Second)
	for a.running.Load() {
		select {
		case <-deadline:
			t.Fatal("full output buffer prevented cancellation cleanup")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	// The caller deliberately did not drain during cancellation.
	for range events {
	}
	if g := requireGoal(t, gs, sid); g.TokensUsed != 4 {
		t.Fatalf("usage lost on cancelled full buffer: %#v", g)
	}
	q := &goalTestProvider{complete: func(context.Context, providers.Request) (providers.Response, error) {
		return providers.Response{Content: "done"}, nil
	}}
	if _, e := goalAgent(q, gs, sid).Run(ctx, "resume"); e != nil {
		t.Fatalf("admission leaked: %v", e)
	}
}

func TestGoalExecution_MigratesLegacyGoalColumns(t *testing.T) {
	db := openGoalDB(t)
	if _, e := db.Exec(`CREATE TABLE goals (session_id TEXT PRIMARY KEY, text TEXT NOT NULL DEFAULT '', active BOOLEAN NOT NULL DEFAULT true, created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`); e != nil {
		t.Fatal(e)
	}
	if _, e := db.Exec(`INSERT INTO goals(session_id,text) VALUES ('legacy','ship')`); e != nil {
		t.Fatal(e)
	}
	gs, e := NewGoalStore(db)
	if e != nil {
		t.Fatalf("legacy migration: %v", e)
	}
	if g := requireGoal(t, gs, "legacy"); g.ID == "" || g.TokensUsed != 0 {
		t.Fatalf("legacy migrated goal: %#v", g)
	}
	if _, e := db.Exec(`UPDATE goals SET time_used_seconds=4, time_used_nanoseconds=0 WHERE session_id='legacy'`); e != nil {
		t.Fatal(e)
	}
	gs, e = NewGoalStore(db)
	if e != nil {
		t.Fatal(e)
	}
	if g := requireGoal(t, gs, "legacy"); g.TimeUsedNanoseconds != int64(4*time.Second) {
		t.Fatalf("legacy time backfill: %#v", g)
	}
}

func TestGoalExecution_SlowStreamConsumerStillReceivesFinalResult(t *testing.T) {
	gs := newExecutionGoalStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	sid := "slow-consumer"
	if e := gs.Set(ctx, sid, "ship"); e != nil {
		t.Fatal(e)
	}
	chunks := make([]providers.Chunk, 0, 151)
	for i := 0; i < 150; i++ {
		chunks = append(chunks, providers.Chunk{Content: "x"})
	}
	chunks = append(chunks, providers.Chunk{Done: true, Usage: &providers.Usage{InputTokens: 4}})
	a := goalAgent(&goalTestProvider{stream: goalChunks(chunks...)}, gs, sid)
	events, e := a.Stream(ctx, "work")
	if e != nil {
		t.Fatal(e)
	}
	time.Sleep(20 * time.Millisecond)
	done := 0
	for event := range events {
		if event.Type == EventError {
			t.Fatal(event.Error)
		}
		if event.Type == EventDone {
			done++
		}
	}
	if done != 1 {
		t.Fatalf("final result lost to cleanup cancellation: %d", done)
	}
	if g := requireGoal(t, gs, sid); g.TokensUsed != 4 {
		t.Fatalf("goal usage %#v", g)
	}
}

func TestGoalExecution_AccountingFinalTotalDoesNotDoubleCharge(t *testing.T) {
	gs := newExecutionGoalStore(t)
	ctx := context.Background()
	sid := "mixed-accounting"
	if e := gs.Set(ctx, sid, "ship"); e != nil {
		t.Fatal(e)
	}
	state := NewGoalAccountingState(gs, sid)
	if e := state.RecordTurnStart(ctx); e != nil {
		t.Fatal(e)
	}
	if e := state.RecordUsage(ctx, providers.Usage{InputTokens: 5, OutputTokens: 2}); e != nil {
		t.Fatal(e)
	}
	if e := state.RecordTurnEnd(ctx, 7); e != nil {
		t.Fatal(e)
	}
	if e := state.RecordTurnEnd(ctx, 7); e != nil {
		t.Fatal(e)
	}
	if got := requireGoal(t, gs, sid).TokensUsed; got != 7 {
		t.Fatalf("usage double charged: %d", got)
	}
	if e := state.Check(ctx); !errors.Is(e, context.Canceled) {
		t.Fatalf("closed accounting check: %v", e)
	}
}

func TestGoalExecution_AdmissionFailureDoesNotLeakOrPanic(t *testing.T) {
	gs := newExecutionGoalStore(t)
	ctx := context.Background()
	sid := "admission-failure"
	if e := gs.Set(ctx, sid, "ship"); e != nil {
		t.Fatal(e)
	}
	if _, e := gs.db.Exec(`ALTER TABLE goal_turn_usage ADD CONSTRAINT deny_turn CHECK (false)`); e != nil {
		t.Fatal(e)
	}
	p := &goalTestProvider{complete: func(context.Context, providers.Request) (providers.Response, error) {
		return providers.Response{Content: "done"}, nil
	}}
	if _, e := goalAgent(p, gs, sid).Run(ctx, "work"); e == nil {
		t.Fatal("failed admission should reject work")
	}
	if p.calls.Load() != 0 {
		t.Fatal("provider ran without persisted admission")
	}
	if _, e := gs.db.Exec(`ALTER TABLE goal_turn_usage DROP CONSTRAINT deny_turn`); e != nil {
		t.Fatal(e)
	}
	if _, e := goalAgent(p, gs, sid).Run(ctx, "retry"); e != nil {
		t.Fatalf("failed admission leaked lock: %v", e)
	}
}

func TestGoalExecution_StreamAccountingFailureNeverEmitsDone(t *testing.T) {
	gs := newExecutionGoalStore(t)
	ctx := context.Background()
	sid := "finish-failure"
	if e := gs.Set(ctx, sid, "ship"); e != nil {
		t.Fatal(e)
	}
	// Usage updates succeed, but durable turn finalization must fail. A terminal
	// success event would falsely imply all accounting had been committed.
	if _, e := gs.db.Exec(`ALTER TABLE goal_turn_usage ADD CONSTRAINT reject_end CHECK (ended_at IS NULL)`); e != nil {
		t.Fatal(e)
	}
	p := &goalTestProvider{stream: goalChunks(providers.Chunk{Content: "done", Done: true, Usage: &providers.Usage{InputTokens: 4}})}
	events, e := goalAgent(p, gs, sid).Stream(ctx, "work")
	if e != nil {
		t.Fatal(e)
	}
	done := 0
	var failure error
	for event := range events {
		if event.Type == EventDone {
			done++
		}
		if event.Type == EventError {
			failure = event.Error
		}
	}
	if done != 0 || failure == nil {
		t.Fatalf("accounting failure emitted success: done=%d err=%v", done, failure)
	}
	if got := requireGoal(t, gs, sid).TokensUsed; got != 4 {
		t.Fatalf("reported usage lost: %d", got)
	}
	if _, e := gs.db.Exec(`ALTER TABLE goal_turn_usage DROP CONSTRAINT reject_end`); e != nil {
		t.Fatal(e)
	}
	q := &goalTestProvider{complete: func(context.Context, providers.Request) (providers.Response, error) {
		return providers.Response{Content: "done"}, nil
	}}
	if _, e := goalAgent(q, gs, sid).Run(ctx, "retry"); e != nil {
		t.Fatalf("finalization failure leaked admission: %v", e)
	}
}

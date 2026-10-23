package subagent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type runnerFunc func(context.Context) (*Result, error)

func (f runnerFunc) Run(ctx context.Context) (*Result, error) { return f(ctx) }

func assertNoInventedWork(t *testing.T, result *Result) {
	t.Helper()
	if result == nil || result.Status == "completed" || result.Error == "" {
		t.Fatalf("not a truthful failure: %+v", result)
	}
	if result.TokensIn != 0 || result.TokensOut != 0 || result.CostUSD != 0 || len(result.ToolTrace) > 0 || len(result.FilesRead) > 0 || len(result.FilesWritten) > 0 {
		t.Fatalf("fabricated work: %+v", result)
	}
}

func TestManagerProductionMissingCredentials(t *testing.T) {
	for _, provider := range []string{"xiaomi", "deepseek", "unsupported-provider"} {
		t.Run(provider, func(t *testing.T) {
			t.Setenv("OVERKILL_SUBAGENT_PROVIDER", provider)
			t.Setenv("XIAOMI_API_KEY", "")
			t.Setenv("DEEPSEEK_API_KEY", "")
			m := NewManager(Config{})
			result, err := m.Spawn(context.Background(), &SessionNameTask{FirstMessage: "hello"})
			if err == nil || !strings.Contains(err.Error(), "configure a supported provider") {
				t.Fatalf("missing actionable failure: %v", err)
			}
			assertNoInventedWork(t, result)
			if result.ExitReason != "worker_unavailable" || m.ActiveCount() != 0 {
				t.Fatalf("wrong failure state: %+v active=%d", result, m.ActiveCount())
			}
			cost := m.CostSummary()
			if cost.TotalIn != 0 || cost.TotalOut != 0 || cost.TotalCost != 0 {
				t.Fatalf("fabricated cost: %+v", cost)
			}
		})
	}
}

func TestManagerBatchReportsEveryInitializationFailure(t *testing.T) {
	t.Setenv("OVERKILL_SUBAGENT_PROVIDER", "unsupported-provider")
	m := NewManager(Config{})
	results, err := m.SpawnBatch(context.Background(), []Task{GenericTask{GoalStr: "first"}, GenericTask{GoalStr: "second"}})
	if err == nil || !strings.Contains(err.Error(), "task 0:") || !strings.Contains(err.Error(), "task 1:") {
		t.Fatalf("incomplete batch error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("lost failure results: %v", results)
	}
	for i, result := range results {
		assertNoInventedWork(t, result)
		if result.TaskIndex != i {
			t.Fatal("lost task index")
		}
	}
	if m.ActiveCount() != 0 {
		t.Fatal("failed batch leaked capacity")
	}
}

func TestManagerRejectsUnsupportedTools(t *testing.T) {
	t.Setenv("XIAOMI_API_KEY", "not-used")
	result, err := NewManager(Config{}).Spawn(context.Background(), GenericTask{GoalStr: "edit source", ToolsetVal: []string{"fs", "shell"}})
	if err == nil || !strings.Contains(err.Error(), "tool-capable contract driver") {
		t.Fatalf("unsupported capability accepted: %v", err)
	}
	assertNoInventedWork(t, result)
	if result.ExitReason != "unsupported_capability" {
		t.Fatalf("wrong reason: %+v", result)
	}
}

func TestManagerBatchAggregatesRuntimeErrorsAndPreservesUsage(t *testing.T) {
	m := NewManager(Config{})
	sentinel := errors.New("provider request failed")
	m.SetWorkerFactory(func(cfg RealWorkerConfig) (WorkerRunner, error) {
		return runnerFunc(func(context.Context) (*Result, error) {
			if cfg.TaskIndex == 0 {
				return &Result{Status: "completed", Summary: "real response", TokensIn: 7, TokensOut: 3, CostUSD: 0.02}, nil
			}
			return &Result{TokensIn: 2, TokensOut: 1, CostUSD: 0.01}, sentinel
		}), nil
	})
	results, err := m.SpawnBatch(context.Background(), []Task{GenericTask{GoalStr: "a"}, GenericTask{GoalStr: "b"}})
	if !errors.Is(err, sentinel) || len(results) != 2 {
		t.Fatalf("lost runtime error or results: %v %v", results, err)
	}
	if results[0].Status != "completed" || results[1].Status != "error" || results[1].TokensIn != 2 {
		t.Fatalf("invalid statuses/usage: %+v", results)
	}
	cost := m.CostSummary()
	if cost.ChildrenCount != 2 || cost.TotalIn != 9 || cost.TotalOut != 4 || cost.TotalCost != 0.03 {
		t.Fatalf("incorrect rollup: %+v", cost)
	}
}

func TestManagerSiblingDepthAndCapacityReservation(t *testing.T) {
	m := NewManager(Config{MaxChildren: 3, MaxDepth: 1})
	entered := make(chan struct{}, 3)
	release := make(chan struct{})
	m.SetWorkerFactory(func(RealWorkerConfig) (WorkerRunner, error) {
		return runnerFunc(func(ctx context.Context) (*Result, error) {
			entered <- struct{}{}
			select {
			case <-release:
				return &Result{Status: "completed"}, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}), nil
	})
	first := make(chan error, 1)
	go func() { _, err := m.Spawn(context.Background(), GenericTask{GoalStr: "first"}); first <- err }()
	awaitEntered(t, entered)
	// A two-task batch must fit alongside the first sibling, all at depth one.
	batch := make(chan error, 1)
	go func() {
		_, err := m.SpawnBatch(context.Background(), []Task{GenericTask{GoalStr: "second"}, GenericTask{GoalStr: "third"}})
		batch <- err
	}()
	awaitEntered(t, entered)
	awaitEntered(t, entered)
	for _, child := range m.ActiveChildren() {
		if child.Depth != 1 || child.Cancel == nil {
			t.Fatalf("incorrect sibling state: %+v", child)
		}
	}
	if m.cfg.CurrentDepth != 0 {
		t.Fatal("concurrent siblings changed manager depth")
	}
	if _, err := m.Spawn(context.Background(), GenericTask{GoalStr: "overflow"}); err == nil || !strings.Contains(err.Error(), "too many concurrent") {
		t.Fatalf("capacity exceeded: %v", err)
	}
	close(release)
	for _, done := range []chan error{first, batch} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("worker stuck")
		}
	}
	if m.ActiveCount() != 0 || m.cfg.CurrentDepth != 0 {
		t.Fatal("state not restored")
	}
}

func awaitEntered(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("worker did not start")
	}
}

func TestManagerBatchAdmissionIsAtomic(t *testing.T) {
	m := NewManager(Config{MaxChildren: 2})
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	var constructed atomic.Int32
	m.SetWorkerFactory(func(RealWorkerConfig) (WorkerRunner, error) {
		constructed.Add(1)
		return runnerFunc(func(ctx context.Context) (*Result, error) {
			entered <- struct{}{}
			select {
			case <-release:
				return &Result{Status: "completed"}, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}), nil
	})
	done := make(chan error, 1)
	go func() { _, err := m.Spawn(context.Background(), GenericTask{GoalStr: "occupied"}); done <- err }()
	awaitEntered(t, entered)
	_, err := m.SpawnBatch(context.Background(), []Task{GenericTask{GoalStr: "b"}, GenericTask{GoalStr: "c"}})
	if err == nil || constructed.Load() != 1 || m.ActiveCount() != 1 {
		t.Fatalf("partial admission occurred: %v constructed=%d active=%d", err, constructed.Load(), m.ActiveCount())
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestManagerTimeoutCancelAndPanicCleanup(t *testing.T) {
	for _, scenario := range []string{"timeout", "cancel", "panic", "nil-runner", "nil-result"} {
		t.Run(scenario, func(t *testing.T) {
			m := NewManager(Config{ChildTimeout: 20 * time.Millisecond})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			entered := make(chan struct{}, 1)
			m.SetWorkerFactory(func(RealWorkerConfig) (WorkerRunner, error) {
				if scenario == "nil-runner" {
					return nil, nil
				}
				return runnerFunc(func(ctx context.Context) (*Result, error) {
					if scenario == "panic" {
						panic("bad provider")
					}
					if scenario == "nil-result" {
						return nil, nil
					}
					entered <- struct{}{}
					<-ctx.Done()
					return nil, ctx.Err()
				}), nil
			})
			if scenario == "cancel" {
				go func() { <-entered; cancel() }()
			}
			result, err := m.Spawn(ctx, GenericTask{GoalStr: "test"})
			if err == nil {
				t.Fatal("failure returned nil error")
			}
			assertNoInventedWork(t, result)
			if scenario == "timeout" && (!errors.Is(err, context.DeadlineExceeded) || result.Status != "timeout") {
				t.Fatalf("wrong timeout: %+v %v", result, err)
			}
			if scenario == "cancel" && (!errors.Is(err, context.Canceled) || result.Status != "interrupted") {
				t.Fatalf("wrong cancellation: %+v %v", result, err)
			}
			if m.ActiveCount() != 0 {
				t.Fatal("failure leaked capacity")
			}
		})
	}
}

func TestManagerAutoSplitPreservesFailedBatchResults(t *testing.T) {
	m := NewManager(Config{MaxChildren: 1, MaxTasksPerChild: 4})
	var invoked atomic.Int32
	m.SetWorkerFactory(func(RealWorkerConfig) (WorkerRunner, error) {
		n := invoked.Add(1)
		return runnerFunc(func(context.Context) (*Result, error) {
			if n == 2 {
				return nil, fmt.Errorf("failure in second batch")
			}
			return &Result{Status: "completed"}, nil
		}), nil
	})
	results, err := m.SpawnBatchAutoSplit(context.Background(), []Task{GenericTask{GoalStr: "a"}, GenericTask{GoalStr: "b"}, GenericTask{GoalStr: "c"}})
	if err == nil || len(results) != 2 || invoked.Load() != 2 {
		t.Fatalf("lost partial results or continued after failure: %v %v calls=%d", results, err, invoked.Load())
	}
	if results[0].TaskIndex != 0 || results[1].TaskIndex != 1 || results[1].Status != "error" {
		t.Fatalf("wrong partial state: %+v", results)
	}
}

func TestManagerPrevalidatesEntireAutoSplit(t *testing.T) {
	m := newSimulatedManager(Config{MaxChildren: 1})
	for _, tasks := range [][]Task{{GenericTask{GoalStr: "a"}, nil}, {GenericTask{GoalStr: "a"}, (*SessionNameTask)(nil)}, {GenericTask{GoalStr: "a"}, GenericTask{}}} {
		if _, err := m.SpawnBatchAutoSplit(context.Background(), tasks); err == nil {
			t.Fatal("invalid input accepted")
		}
	}
	if m.CostSummary().ChildrenCount != 0 {
		t.Fatal("ran earlier batches before validating later input")
	}
}

type blockingStepDriver struct {
	fakeDriver
	entered chan struct{}
}

func (d *blockingStepDriver) Step(ctx context.Context) StepResult {
	d.entered <- struct{}{}
	<-ctx.Done()
	return StepResult{Err: ctx.Err()}
}

func TestManagerSharesCapacityWithAutonomousChildren(t *testing.T) {
	m := newSimulatedManager(Config{MaxChildren: 1})
	driver := &blockingStepDriver{fakeDriver: fakeDriver{max: 100000}, entered: make(chan struct{}, 1)}
	contract := runnerContract([]string{"pending"})
	id, err := m.SpawnContract(context.Background(), contract, driver, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer m.AutonomousCancel(id)
	awaitEntered(t, driver.entered)
	if _, err := m.Spawn(context.Background(), GenericTask{GoalStr: "overflow"}); err == nil {
		t.Fatal("worker ignored autonomous capacity")
	}
	second := runnerContract([]string{"pending"})
	second.ID = "second"
	if _, err := m.SpawnContract(context.Background(), second, driver, "", nil); err == nil {
		t.Fatal("autonomous limit exceeded")
	}
	m.AutonomousCancel(id)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := m.AutonomousWait(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Spawn(context.Background(), GenericTask{GoalStr: "after completion"}); err != nil {
		t.Fatalf("completed entry still consumed capacity: %v", err)
	}
}

func TestManagerAutonomousAdmissionCountsRunningWorkers(t *testing.T) {
	m := NewManager(Config{MaxChildren: 1})
	entered := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.SetWorkerFactory(func(RealWorkerConfig) (WorkerRunner, error) {
		return runnerFunc(func(ctx context.Context) (*Result, error) { entered <- struct{}{}; <-ctx.Done(); return nil, ctx.Err() }), nil
	})
	done := make(chan struct{})
	go func() { defer close(done); _, _ = m.Spawn(ctx, GenericTask{GoalStr: "running"}) }()
	awaitEntered(t, entered)
	if _, err := m.SpawnContract(ctx, runnerContract([]string{"pending"}), &fakeDriver{max: 100000}, "", nil); err == nil {
		t.Fatal("autonomous child ignored occupied worker slot")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not stop")
	}
}

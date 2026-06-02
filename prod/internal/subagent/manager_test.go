package subagent

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

// --------------------------------------------------------------------------
// Test 1: Spawn a SessionNameTask, verify status="completed"
// --------------------------------------------------------------------------

// TestRealWorker_Run_ErrorPropagation proves bug #22:
// When the real worker fails, the error must be returned to the caller,
// NOT silently swallowed with a nil error.
func TestRealWorker_Run_ErrorPropagation(t *testing.T) {
	mp := &mockSubagentProvider{failAll: true}
	rw := &RealWorker{
		cfg: RealWorkerConfig{
			Goal:      "test",
			Context:   "test",
			MaxSteps:  1,
			TaskIndex: 0,
			MaxTokens: 100,
		},
		provider: mp,
	}

	_, err := rw.Run(context.Background())
	if err == nil {
		t.Fatal("BUG #22: expected error from failed worker, got nil — error silently swallowed")
	}
}

// mockSubagentProvider is a minimal providers.Provider for testing RealWorker.
type mockSubagentProvider struct {
	failAll bool
}

func (m *mockSubagentProvider) Complete(_ context.Context, _ providers.Request) (providers.Response, error) {
	if m.failAll {
		return providers.Response{}, fmt.Errorf("provider unavailable")
	}
	return providers.Response{
		Content: "ok",
		Usage:   providers.Usage{InputTokens: 10, OutputTokens: 5},
	}, nil
}

func (m *mockSubagentProvider) Stream(_ context.Context, _ providers.Request) (<-chan providers.Chunk, error) {
	return nil, nil
}

func (m *mockSubagentProvider) Models() []providers.Model {
	return nil
}

func (m *mockSubagentProvider) Name() string { return "mock" }

func TestManager_SpawnSingle(t *testing.T) {
	m := NewManager(Config{
		MaxChildren:  5,
		MaxDepth:     2,
		ChildTimeout: 5 * time.Second,
	})

	task := &SessionNameTask{FirstMessage: "Help me write a sorting algorithm"}

	res, err := m.Spawn(context.Background(), task)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Status != "completed" {
		t.Errorf("Status = %q, want %q", res.Status, "completed")
	}
	if !strings.Contains(res.Summary, task.Goal()) {
		t.Errorf("Summary = %q, should contain goal text", res.Summary)
	}
}

// --------------------------------------------------------------------------
// Test 2: SpawnBatch with 3 tasks, verify len=3 and TaskIndex order
// --------------------------------------------------------------------------

func TestManager_SpawnBatch(t *testing.T) {
	m := NewManager(Config{
		MaxChildren:  5,
		MaxDepth:     2,
		ChildTimeout: 5 * time.Second,
	})

	tasks := []Task{
		&SessionNameTask{FirstMessage: "Refactor auth"},
		&SessionNameTask{FirstMessage: "Fix race condition"},
		&SessionNameTask{FirstMessage: "Add caching"},
	}

	results, err := m.SpawnBatch(context.Background(), tasks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(results) != 3 {
		t.Fatalf("len(results) = %d, want 3", len(results))
	}

	for i, r := range results {
		if r.TaskIndex != i {
			t.Errorf("results[%d].TaskIndex = %d, want %d", i, r.TaskIndex, i)
		}
		if r.Status != "completed" {
			t.Errorf("results[%d].Status = %q, want %q", i, r.Status, "completed")
		}
	}
}

// --------------------------------------------------------------------------
// Test 3: Depth limit — CurrentDepth=2, MaxDepth=2 → error
// --------------------------------------------------------------------------

func TestManager_DepthLimit(t *testing.T) {
	m := NewManager(Config{
		MaxDepth:     2,
		MaxChildren:  5,
		ChildTimeout: 5 * time.Second,
		CurrentDepth: 2,
	})

	task := &SessionNameTask{FirstMessage: "Should not run"}

	_, err := m.Spawn(context.Background(), task)
	if err == nil {
		t.Fatal("expected error for depth limit, got nil")
	}
	if !strings.Contains(err.Error(), "depth limit") {
		t.Errorf("error = %q, should contain 'depth limit'", err.Error())
	}
}

// --------------------------------------------------------------------------
// Test 4: Capacity limit — MaxChildren=1, SpawnBatch with 2 tasks → error
// --------------------------------------------------------------------------

func TestManager_CapacityLimit(t *testing.T) {
	m := NewManager(Config{
		MaxChildren:  1,
		MaxDepth:     2,
		ChildTimeout: 5 * time.Second,
	})

	tasks := []Task{
		&SessionNameTask{FirstMessage: "Task A"},
		&SessionNameTask{FirstMessage: "Task B"},
	}

	_, err := m.SpawnBatch(context.Background(), tasks)
	if err == nil {
		t.Fatal("expected error for capacity limit, got nil")
	}
	if !strings.Contains(err.Error(), "too many concurrent children") {
		t.Errorf("error = %q, should contain 'too many concurrent children'", err.Error())
	}
}

// --------------------------------------------------------------------------
// Test 5: Validation fails — CompactionTask with empty Messages
// --------------------------------------------------------------------------

func TestManager_ValidationFails(t *testing.T) {
	m := NewManager(Config{
		MaxChildren:  5,
		MaxDepth:     2,
		ChildTimeout: 5 * time.Second,
	})

	task := &CompactionTask{Messages: nil, TargetTokens: 500}

	_, err := m.Spawn(context.Background(), task)
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if !strings.Contains(err.Error(), "validation") {
		t.Errorf("error = %q, should contain 'validation'", err.Error())
	}
}

// --------------------------------------------------------------------------
// Test 6: ActiveChildren — starts at 0
// --------------------------------------------------------------------------

func TestManager_ActiveChildren(t *testing.T) {
	m := NewManager(Config{
		MaxChildren:  5,
		MaxDepth:     2,
		ChildTimeout: 5 * time.Second,
	})

	if count := m.ActiveCount(); count != 0 {
		t.Errorf("ActiveCount() = %d, want 0", count)
	}

	// Spawn a task and confirm it goes back to 0 after completion.
	task := &SessionNameTask{FirstMessage: "Hello"}
	_, err := m.Spawn(context.Background(), task)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count := m.ActiveCount(); count != 0 {
		t.Errorf("ActiveCount() after spawn = %d, want 0", count)
	}
}

// --------------------------------------------------------------------------
// Test 7: CostRollup — SpawnBatch 2 tasks, verify ChildrenCount == 2
// --------------------------------------------------------------------------

func TestManager_CostRollup(t *testing.T) {
	m := NewManager(Config{
		MaxChildren:  5,
		MaxDepth:     2,
		ChildTimeout: 5 * time.Second,
	})

	tasks := []Task{
		&SessionNameTask{FirstMessage: "First task"},
		&SessionNameTask{FirstMessage: "Second task"},
	}

	_, err := m.SpawnBatch(context.Background(), tasks)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	summary := m.CostSummary()
	if summary.ChildrenCount != 2 {
		t.Errorf("ChildrenCount = %d, want 2", summary.ChildrenCount)
	}
	if summary.TotalIn <= 0 {
		t.Errorf("TotalIn = %d, want > 0", summary.TotalIn)
	}
	if summary.TotalOut <= 0 {
		t.Errorf("TotalOut = %d, want > 0", summary.TotalOut)
	}
	if summary.TotalCost <= 0 {
		t.Errorf("TotalCost = %f, want > 0", summary.TotalCost)
	}
}

// --------------------------------------------------------------------------
// Test 8: CancelAll — cancel context while spawn is running, verify no panic
// --------------------------------------------------------------------------

func TestManager_CancelAll(t *testing.T) {
	m := NewManager(Config{
		MaxChildren:  5,
		MaxDepth:     2,
		ChildTimeout: 10 * time.Second,
	})

	ctx, cancel := context.WithCancel(context.Background())

	var panicked atomic.Bool

	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() {
			if r := recover(); r != nil {
				panicked.Store(true)
			}
		}()

		task := &SessionNameTask{FirstMessage: "Cancel test"}
		// Result is allowed to be any status (interrupted, completed, etc.)
		_, _ = m.Spawn(ctx, task)
	}()

	// Cancel shortly after launch.
	time.Sleep(20 * time.Millisecond)
	cancel()

	<-done

	if panicked.Load() {
		t.Error("Spawn panicked on context cancellation")
	}
}

package subagent

import (
	"context"
	"fmt"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
)

// Config controls the limits and behaviour of a Manager.
type Config struct {
	MaxDepth     int
	MaxChildren  int
	ChildTimeout time.Duration
	CurrentDepth int // internal depth counter
}

// ChildRef is a live handle to a spawned sub-agent.
type ChildRef struct {
	ID        string
	Goal      string
	Model     string
	Status    string
	StartedAt time.Time
	Cancel    context.CancelFunc
	Result    *Result
	Depth     int
	Role      Role
}

// Manager coordinates sub-agent spawning with depth/capacity limits and
// parallel execution. All public methods are safe for concurrent use.
type Manager struct {
	mu          sync.RWMutex
	cfg         Config
	children    map[string]*ChildRef
	fileState   *FileStateTracker
	costTracker *CostRollup
}

// NewManager creates a Manager with sensible defaults.
//
// Defaults: MaxDepth=2, MaxChildren=3, ChildTimeout=120s (when <= 0).
func NewManager(cfg Config) *Manager {
	if cfg.MaxDepth <= 0 {
		cfg.MaxDepth = 2
	}
	if cfg.MaxChildren <= 0 {
		cfg.MaxChildren = 3
	}
	if cfg.ChildTimeout <= 0 {
		cfg.ChildTimeout = 120 * time.Second
	}

	return &Manager{
		cfg:         cfg,
		children:    make(map[string]*ChildRef),
		fileState:   NewFileStateTracker(),
		costTracker: NewCostRollup("manager"),
	}
}

// Spawn runs a single child task subject to depth and capacity limits.
func (m *Manager) Spawn(ctx context.Context, task Task) (*Result, error) {
	// 1. Validate the task.
	if err := task.Validate(); err != nil {
		return nil, fmt.Errorf("validation: %w", err)
	}

	// 2. Depth check.
	if m.cfg.CurrentDepth >= m.cfg.MaxDepth {
		return nil, fmt.Errorf("depth limit reached (depth=%d, max=%d)", m.cfg.CurrentDepth, m.cfg.MaxDepth)
	}

	// 3. Capacity check under lock.
	m.mu.Lock()
	if len(m.children) >= m.cfg.MaxChildren {
		m.mu.Unlock()
		return nil, fmt.Errorf("too many concurrent children (%d/%d)", len(m.children), m.cfg.MaxChildren)
	}

	// 4. Register child.
	childID := fmt.Sprintf("child-%d", time.Now().UnixNano())
	childCtx, cancel := context.WithTimeout(ctx, m.cfg.ChildTimeout)

	ref := &ChildRef{
		ID:        childID,
		Goal:      task.Goal(),
		Model:     task.Model(),
		Status:    "running",
		StartedAt: time.Now(),
		Cancel:    cancel,
		Depth:     m.cfg.CurrentDepth + 1,
		Role:      RoleWorker,
	}
	m.children[childID] = ref
	m.mu.Unlock()

	// 5. Run the worker (lock released).
	w := NewWorker(WorkerConfig{
		Goal:      task.Goal(),
		Context:   task.Context(),
		MaxSteps:  task.MaxIterations(),
		Timeout:   m.cfg.ChildTimeout,
		TaskIndex: 0,
	})

	result, _ := w.Run(childCtx)
	cancel()

	// 6. Unregister child.
	m.mu.Lock()
	delete(m.children, childID)
	m.mu.Unlock()

	// 7. Track cost.
	m.costTracker.AddChild(result)

	return result, nil
}

// SpawnBatch runs multiple tasks in parallel using an errgroup.
// All tasks are validated before any are launched.
func (m *Manager) SpawnBatch(ctx context.Context, tasks []Task) ([]*Result, error) {
	// 1. Preliminary capacity check.
	if len(tasks) > m.cfg.MaxChildren {
		return nil, fmt.Errorf("too many concurrent children: %d tasks exceed limit of %d", len(tasks), m.cfg.MaxChildren)
	}

	// 2. Validate ALL tasks upfront.
	for i, t := range tasks {
		if err := t.Validate(); err != nil {
			return nil, fmt.Errorf("task %d validation: %w", i, err)
		}
	}

	// 3. Depth check.
	if m.cfg.CurrentDepth >= m.cfg.MaxDepth {
		return nil, fmt.Errorf("depth limit reached (depth=%d, max=%d)", m.cfg.CurrentDepth, m.cfg.MaxDepth)
	}

	results := make([]*Result, len(tasks))

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(m.cfg.MaxChildren)

	for i, t := range tasks {
		i, t := i, t // capture loop variables

		g.Go(func() error {
			w := NewWorker(WorkerConfig{
				Goal:      t.Goal(),
				Context:   t.Context(),
				MaxSteps:  t.MaxIterations(),
				Timeout:   m.cfg.ChildTimeout,
				TaskIndex: i,
			})

			res, _ := w.Run(gctx)
			results[i] = res
			return nil
		})
	}

	// Wait for all goroutines to complete.
	if err := g.Wait(); err != nil {
		return nil, err
	}

	// Roll up costs.
	for _, r := range results {
		m.costTracker.AddChild(r)
	}

	return results, nil
}

// ActiveCount returns the number of currently running children.
func (m *Manager) ActiveCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.children)
}

// CostSummary returns a snapshot of the aggregated child costs.
func (m *Manager) CostSummary() RollupSummary {
	return m.costTracker.Summary()
}

// FileState returns the manager's file-state tracker.
func (m *Manager) FileState() *FileStateTracker {
	return m.fileState
}

// ActiveChildren returns a snapshot of currently running children. Safe for
// concurrent use; the returned slice is owned by the caller.
func (m *Manager) ActiveChildren() []ChildRef {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]ChildRef, 0, len(m.children))
	for _, c := range m.children {
		out = append(out, *c)
	}
	return out
}

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

	// Contract-based autonomous children, keyed by contract.ID.
	autonomous map[string]*autonomousEntry

	// driverFactory builds a fresh StepDriver for a given contract. When
	// nil the contract path on delegate_task returns an error.
	driverFactory func(*Contract) (StepDriver, error)

	// failureSink, when set, is invoked when a contract terminates in any
	// non-completed state. Used by cmd/ethos to push delegation_failure
	// alerts into the journal AlertStore. Best-effort: fired in a goroutine.
	failureSink HandoffFailureSink
}

// HandoffFailureSink receives a structured record of every cross-agent
// failure so the parent's journal can attribute the fault to the delegation
// decision, not just to the child.
type HandoffFailureSink interface {
	OnDelegationFailure(parentSession string, contract *Contract, report *FinalReport, err error)
}

// SetFailureSink wires a delegation-failure observer (master plan §5.3).
// Pass nil to clear.
func (m *Manager) SetFailureSink(s HandoffFailureSink) {
	m.mu.Lock()
	m.failureSink = s
	m.mu.Unlock()
}

// SetDriverFactory wires a per-contract StepDriver builder. cmd/ethos calls
// this once at startup with a closure that constructs a clean child agent.
func (m *Manager) SetDriverFactory(f func(*Contract) (StepDriver, error)) {
	m.mu.Lock()
	m.driverFactory = f
	m.mu.Unlock()
}

// HasDriverFactory reports whether a factory has been configured.
func (m *Manager) HasDriverFactory() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.driverFactory != nil
}

// SpawnFromFactory uses the configured factory to build a driver and then
// SpawnContract under it. Returns an error if no factory is set.
func (m *Manager) SpawnFromFactory(ctx context.Context, c *Contract, workdir string) (string, error) {
	m.mu.RLock()
	f := m.driverFactory
	m.mu.RUnlock()
	if f == nil {
		return "", fmt.Errorf("subagent: no driver factory configured")
	}
	d, err := f(c)
	if err != nil {
		return "", fmt.Errorf("subagent: driver build: %w", err)
	}
	return m.SpawnContract(ctx, c, d, workdir, nil)
}

// autonomousEntry tracks one in-flight or completed contract-based child.
type autonomousEntry struct {
	contract *Contract
	runner   *AutonomousRunner
	cancel   context.CancelFunc
	done     chan struct{} // closed when Run returns
	report   *FinalReport
	runErr   error
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
		autonomous:  make(map[string]*autonomousEntry),
	}
}

// SpawnContract registers a contract-driven autonomous child and runs it in a
// background goroutine. Returns the contract ID immediately; the parent calls
// Status / Wait / Cancel against that ID.
//
// Workdir, if non-empty, is the working directory passed to AcceptanceRunner.
// Runner overrides the default acceptance runner (useful in tests).
func (m *Manager) SpawnContract(ctx context.Context, contract *Contract, driver StepDriver, workdir string, runner AcceptanceRunner) (string, error) {
	if contract == nil {
		return "", fmt.Errorf("subagent: contract is required")
	}
	if err := contract.Validate(); err != nil {
		return "", err
	}

	m.mu.Lock()
	if _, exists := m.autonomous[contract.ID]; exists {
		m.mu.Unlock()
		return "", fmt.Errorf("subagent: contract %q already registered", contract.ID)
	}
	if len(m.autonomous) >= m.cfg.MaxChildren {
		m.mu.Unlock()
		return "", fmt.Errorf("subagent: too many autonomous children (%d/%d)", len(m.autonomous), m.cfg.MaxChildren)
	}

	cctx, cancel := context.WithCancel(ctx)
	r, err := NewAutonomousRunner(AutonomousConfig{
		Contract: contract,
		Driver:   driver,
		Workdir:  workdir,
		Runner:   runner,
	})
	if err != nil {
		cancel()
		m.mu.Unlock()
		return "", err
	}

	entry := &autonomousEntry{
		contract: contract,
		runner:   r,
		cancel:   cancel,
		done:     make(chan struct{}),
	}
	m.autonomous[contract.ID] = entry
	m.mu.Unlock()

	go func() {
		rep, err := r.Run(cctx)
		m.mu.Lock()
		entry.report = rep
		entry.runErr = err
		sink := m.failureSink
		m.mu.Unlock()
		close(entry.done)
		// Cross-agent fault attribution (master plan §5.3): any non-completed
		// terminal state means the *delegation decision* deserves a journal
		// entry. The sink converts this into AlertDelegationFailed.
		if sink != nil && (err != nil || (rep != nil && rep.Status != "completed")) {
			go func() {
				defer func() { _ = recover() }()
				sink.OnDelegationFailure(contract.ParentSession, contract, rep, err)
			}()
		}
	}()

	return contract.ID, nil
}

// AutonomousStatus returns the live status of a contract-driven child. The
// boolean is false when no such child exists.
func (m *Manager) AutonomousStatus(id string) (StatusReport, bool) {
	m.mu.RLock()
	entry, ok := m.autonomous[id]
	m.mu.RUnlock()
	if !ok {
		return StatusReport{}, false
	}
	return entry.runner.Status(), true
}

// AutonomousReport returns the FinalReport once the child has finished. When
// running is true, the child is still in flight.
func (m *Manager) AutonomousReport(id string) (report *FinalReport, running bool, err error) {
	m.mu.RLock()
	entry, ok := m.autonomous[id]
	m.mu.RUnlock()
	if !ok {
		return nil, false, fmt.Errorf("subagent: no contract %q", id)
	}
	select {
	case <-entry.done:
		m.mu.RLock()
		defer m.mu.RUnlock()
		return entry.report, false, entry.runErr
	default:
		return nil, true, nil
	}
}

// AutonomousWait blocks until the child finishes, or ctx is cancelled.
func (m *Manager) AutonomousWait(ctx context.Context, id string) (*FinalReport, error) {
	m.mu.RLock()
	entry, ok := m.autonomous[id]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("subagent: no contract %q", id)
	}
	select {
	case <-entry.done:
		m.mu.RLock()
		defer m.mu.RUnlock()
		return entry.report, entry.runErr
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// AutonomousCancel signals the child runner to stop and unblocks AutonomousWait.
// Returns false when no such child exists.
func (m *Manager) AutonomousCancel(id string) bool {
	m.mu.RLock()
	entry, ok := m.autonomous[id]
	m.mu.RUnlock()
	if !ok {
		return false
	}
	entry.runner.Cancel()
	entry.cancel()
	return true
}

// AutonomousList returns the IDs of all registered contract-driven children.
func (m *Manager) AutonomousList() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]string, 0, len(m.autonomous))
	for id := range m.autonomous {
		out = append(out, id)
	}
	return out
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

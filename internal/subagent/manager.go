package subagent

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"
)

// TaskRouter picks a model for a sub-agent task. Satisfied by
// *routing.SmartRouter via a thin adapter at the wiring site.
// Lives here (not in routing) to avoid import cycles:
//
//	agent → subagent → routing → agent  ✗
type TaskRouter interface {
	RouteTask(ctx context.Context, goal, contextStr string) (modelID, provider string, ok bool)
}

// Config controls the limits and behaviour of a Manager.
type Config struct {
	MaxDepth     int
	MaxChildren  int
	ChildTimeout time.Duration
	CurrentDepth int // internal depth counter

	// MaxTasksPerChild is the soft limit on how many tasks can be
	// dispatched in a single SpawnBatch call. When the input exceeds
	// this, SpawnBatchAutoSplit automatically splits into sequential
	// batches. Defaults to MaxChildren when unset (0).
	MaxTasksPerChild int
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

	// router, when set, picks the cheapest capable model per sub-agent task.
	// Falls back to the main model when nil or on routing failure.
	router TaskRouter

	// registry, when set, provides file-based agent discovery and
	// auto-selection via SelectBest. Set via SetRegistry at boot.
	registry *AgentRegistry

	// failureSink, when set, is invoked when a contract terminates in any
	// non-completed state. Used by cmd/overkill to push delegation_failure
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

// SetRouter wires a task-level model router. Pass nil to clear.
func (m *Manager) SetRouter(r TaskRouter) {
	m.mu.Lock()
	m.router = r
	m.mu.Unlock()
}

// SetRegistry wires an agent registry for file-based agent discovery
// and auto-selection. Pass nil to clear.
func (m *Manager) SetRegistry(r *AgentRegistry) {
	// Store for use by Spawn and delegate_task tool.
	// The registry is consulted when no explicit agent name is provided.
	m.mu.Lock()
	m.registry = r
	m.mu.Unlock()
}

// SetDriverFactory wires a per-contract StepDriver builder. cmd/overkill calls
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
		// C2 fix: only count RUNNING children against capacity.
		// Completed children are evicted after a 1-hour grace window
		// but must not block new spawns during that window.
		if m.runningAutonomousCount() >= m.cfg.MaxChildren {
			m.mu.Unlock()
			return "", fmt.Errorf("subagent: too many autonomous children (%d/%d)", m.runningAutonomousCount(), m.cfg.MaxChildren)
		}
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
		defer func() {
			if r := recover(); r != nil {
				m.mu.Lock()
				entry.report = &FinalReport{
					ContractID: contract.ID,
					Status:     "failed",
					Reason:     fmt.Sprintf("subagent panicked: %v", r),
				}
				entry.runErr = fmt.Errorf("subagent panicked: %v", r)
				sink := m.failureSink
				m.mu.Unlock()
				close(entry.done)
				if sink != nil {
					go func() {
						defer func() { _ = recover() }()
						sink.OnDelegationFailure(contract.ParentSession, contract, entry.report, entry.runErr)
					}()
				}
				return
			}
		}()
		rep, err := r.Run(cctx)
		m.mu.Lock()
		entry.report = rep
		entry.runErr = err
		sink := m.failureSink
		m.mu.Unlock()
		close(entry.done)

		// Track cost for autonomous children — convert FinalReport tokens
		// into a Result for the CostRollup (H-25 fix).
		if rep != nil && rep.TokensUsed > 0 {
			costResult := &Result{
				Status:    rep.Status,
				TokensIn:  int64(rep.TokensUsed / 2),
				TokensOut: int64(rep.TokensUsed / 2),
				CostUSD:   estimateCostUSD("", int64(rep.TokensUsed/2), int64(rep.TokensUsed/2)),
			}
			m.costTracker.AddChild(costResult)
		}
		// Evict the entry after a 1h grace window so callers still
		// have time to fetch AutonomousReport, but the map doesn't
		// grow forever. Without this, every long-running deployment
		// accumulated O(total-contracts-ever) entries, eventually
		// crossing m.cfg.MaxChildren and refusing new dispatches.
		go func() {
			t := time.NewTimer(1 * time.Hour)
			defer t.Stop()
			select {
			case <-t.C:
			}
			m.mu.Lock()
			if current, ok := m.autonomous[contract.ID]; ok && current == entry {
				delete(m.autonomous, contract.ID)
			}
			m.mu.Unlock()
		}()
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

// CostSummary returns a snapshot of the aggregated child costs.
func (m *Manager) Spawn(ctx context.Context, task Task) (*Result, error) {
	// 0. Nil guard — prevent nil pointer dereference on task.Validate().
	if task == nil {
		return nil, fmt.Errorf("subagent: task is nil")
	}

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
	childID := fmt.Sprintf("child-%s", uuid.New().String())
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

	// 5. Run the worker — route through SmartRouter for model selection,
	// then try real LLM, fall back to stub.

	// Pick model via SmartRouter (complexity-based, cost-aware).
	model := task.Model()
	provider := ""
	m.mu.RLock()
	router := m.router
	m.mu.RUnlock()
	if router != nil && model == "" {
		// Only route when the task didn't pin a specific model.
		if mid, prov, ok := router.RouteTask(childCtx, task.Goal(), task.Context()); ok {
			model = mid
			provider = prov
		}
	}

	var result *Result

	rw, err := NewRealWorker(RealWorkerConfig{
		Goal:     task.Goal(),
		Context:  task.Context(),
		MaxSteps: task.MaxIterations(),
		Timeout:  m.cfg.ChildTimeout,
		Model:    model,
		Provider: provider,
		TaskIndex: 0, // single spawn, always index 0
	})
	if err == nil {
		result, err = rw.Run(childCtx)
		if err != nil {
			result = &Result{Status: "error", Summary: "Worker error: " + err.Error(), Error: err.Error(), ExitReason: "worker_error"}
		}
	} else {
		// Fall back to stub worker when XIAOMI_API_KEY not set.
		w := NewWorker(WorkerConfig{
			Goal:      task.Goal(),
			Context:   task.Context(),
			MaxSteps:  task.MaxIterations(),
			Timeout:   m.cfg.ChildTimeout,
			TaskIndex: 0,
		})
		result, _ = w.Run(childCtx)
	}
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

		// Register child under capacity lock.
		childID := fmt.Sprintf("child-batch-%s-%d", uuid.New().String(), i)
		m.mu.Lock()
		ref := &ChildRef{
			ID:        childID,
			Goal:      t.Goal(),
			Model:     t.Model(),
			Status:    "running",
			StartedAt: time.Now(),
			Depth:     m.cfg.CurrentDepth + 1,
			Role:      RoleWorker,
		}
		m.children[childID] = ref
		m.mu.Unlock()

		g.Go(func() error {
			defer func() {
				m.mu.Lock()
				delete(m.children, childID)
				m.mu.Unlock()
			}()

			// Route through SmartRouter for model selection.
			model := t.Model()
			provider := ""
			m.mu.RLock()
			router := m.router
			m.mu.RUnlock()
			if router != nil && model == "" {
				if mid, prov, ok := router.RouteTask(gctx, t.Goal(), t.Context()); ok {
					model = mid
					provider = prov
				}
			}

			// Try real LLM first, fall back to stub.
			var res *Result
			rw, rwErr := NewRealWorker(RealWorkerConfig{
				Goal:     t.Goal(),
				Context:  t.Context(),
				MaxSteps: t.MaxIterations(),
				Timeout:  m.cfg.ChildTimeout,
				Model:    model,
				Provider: provider,
				TaskIndex: i,
			})
			if rwErr == nil {
				res, rwErr = rw.Run(gctx)
				if rwErr != nil {
					res = &Result{Status: "error", Error: rwErr.Error(), ExitReason: "worker_error"}
				}
			} else {
				// Fall back to stub worker when no API key available.
				w := NewWorker(WorkerConfig{
					Goal:      t.Goal(),
					Context:   t.Context(),
					MaxSteps:  t.MaxIterations(),
					Timeout:   m.cfg.ChildTimeout,
					TaskIndex: i,
				})
				res, _ = w.Run(gctx)
			}
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

// ActiveCount returns the number of currently running children (Spawn path).
func (m *Manager) ActiveCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.children)
}

// runningAutonomousCount returns the number of autonomous children that are
// still running (not yet completed). Used for capacity checks (C2 fix).
func (m *Manager) runningAutonomousCount() int {
	count := 0
	for _, entry := range m.autonomous {
		select {
		case <-entry.done:
			// Already completed — don't count against capacity.
		default:
			count++
		}
	}
	return count
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

// batchSize returns the effective per-batch task limit.
func (m *Manager) batchSize() int {
	if m.cfg.MaxTasksPerChild > 0 {
		return m.cfg.MaxTasksPerChild
	}
	if m.cfg.MaxChildren > 0 {
		return m.cfg.MaxChildren
	}
	return 4
}

// SpawnBatchAutoSplit is like SpawnBatch but automatically splits oversized
// task lists into sequential batches. When len(tasks) > batchSize(), tasks are
// sliced into batches of batchSize(), each run via SpawnBatch in order, and
// results are concatenated. This prevents single sub-agents from hitting
// max_iterations when the parent dumps 9+ tasks on them.
func (m *Manager) SpawnBatchAutoSplit(ctx context.Context, tasks []Task) ([]*Result, error) {
	bs := m.batchSize()
	if len(tasks) <= bs {
		return m.SpawnBatch(ctx, tasks)
	}

	var all []*Result
	for start := 0; start < len(tasks); start += bs {
		end := min(start+bs, len(tasks))
		batch := tasks[start:end]
		results, err := m.SpawnBatch(ctx, batch)
		if err != nil {
			return all, fmt.Errorf("batch [%d-%d]: %w", start+1, end, err)
		}
		all = append(all, results...)
	}
	return all, nil
}

// SpawnDecomposed handles a single goal string that may contain multiple
// independent items. It decomposes the goal via Decomposer — if 2+ items
// are found, they're dispatched as parallel sub-agents through
// SpawnBatchAutoSplit. If only one item (or decomposition fails), it falls
// back to a normal single-task Spawn.
//
// This is the smart delegation path: instead of dumping "fix X in package A,
// also wire Y in package B, and refactor Z" onto one overwhelmed sub-agent,
// the Decomposer splits it into 3 discrete tasks that run in parallel.
func (m *Manager) SpawnDecomposed(ctx context.Context, goal, contextStr string) ([]*Result, error) {
	dc := NewDecomposer()
	items := dc.Decompose(goal)

	if len(items) < 2 {
		// Single item — fall back to normal spawn.
		task := GenericTask{GoalStr: goal, ContextStr: contextStr}
		result, err := m.Spawn(ctx, task)
		if err != nil {
			return nil, err
		}
		return []*Result{result}, nil
	}

	tasks := make([]Task, len(items))
	for i, desc := range items {
		tasks[i] = GenericTask{
			GoalStr:    desc,
			ContextStr: contextStr,
		}
	}
	return m.SpawnBatchAutoSplit(ctx, tasks)
}

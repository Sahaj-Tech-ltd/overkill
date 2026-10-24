package subagent

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
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
	// DefaultTools is the application-granted coding toolset for ordinary delegation.
	DefaultTools []string
	MaxDepth     int
	MaxChildren  int
	ChildTimeout time.Duration
	CurrentDepth int // fixed parent depth; concurrent children are siblings

	// MaxTasksPerChild is the soft limit on how many tasks can be
	// dispatched in a single SpawnBatch call. When the input exceeds
	// this, SpawnBatchAutoSplit automatically splits into sequential
	// batches. Defaults to MaxChildren when unset (0).
	MaxTasksPerChild int
}

// ChildRef is a live handle to a spawned sub-agent.
type ChildRef struct {
	Name      string
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
	communication *CommunicationStore
	mu            sync.RWMutex
	cfg           Config
	children      map[string]*ChildRef
	fileState     *FileStateTracker
	costTracker   *CostRollup

	// Contract-based autonomous children, keyed by contract.ID.
	autonomous map[string]*autonomousEntry

	// driverFactory builds a fresh StepDriver for a given contract. When
	// nil the contract path on delegate_task returns an error.
	driverFactory func(*Contract) (StepDriver, error)

	// router, when set, picks the cheapest capable model per sub-agent task.
	// Falls back to the main model when nil or on routing failure.
	router TaskRouter

	workerFactory  WorkerFactory
	guardedWorkers bool

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

// SetCommunicationStore binds a durable chat-scoped mailbox before execution.
func (m *Manager) SetCommunicationStore(store *CommunicationStore) {
	m.mu.Lock()
	m.communication = store
	m.mu.Unlock()
}
func (m *Manager) CommunicationStore() *CommunicationStore {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.communication
}

// SetFailureSink wires a delegation-failure observer (master plan §5.3).
// Pass nil to clear.
func (m *Manager) SetFailureSink(s HandoffFailureSink) {
	m.mu.Lock()
	m.failureSink = s
	m.mu.Unlock()
}

// SetDefaultToolset changes the trusted session defaults. Model input cannot
// grant a capability: the worker factory independently validates every name.
func (m *Manager) SetDefaultToolset(names []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfg.DefaultTools = append([]string{}, names...)
}
func (m *Manager) DefaultToolset() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.cfg.DefaultTools == nil {
		return []string{"fs", "patch"}
	}
	return append([]string(nil), m.cfg.DefaultTools...)
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
	if cfg.DefaultTools != nil {
		cfg.DefaultTools = append([]string{}, cfg.DefaultTools...)
	}
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
	if err := ctx.Err(); err != nil {
		m.mu.Unlock()
		return "", err
	}
	if m.cfg.CurrentDepth >= m.cfg.MaxDepth {
		m.mu.Unlock()
		return "", fmt.Errorf("subagent: depth limit reached")
	}
	running := len(m.children) + m.runningAutonomousCount()
	if running >= m.cfg.MaxChildren {
		m.mu.Unlock()
		return "", fmt.Errorf("subagent: too many concurrent children (%d/%d)", running, m.cfg.MaxChildren)
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
		defer cancel()
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

// Spawn executes a single task. Initialization and execution failures return a
// failed Result and a non-nil error; no simulated work runs in production.
func (m *Manager) Spawn(ctx context.Context, task Task) (*Result, error) {
	results, err := m.SpawnBatch(ctx, []Task{task})
	if len(results) == 0 {
		return nil, err
	}
	return results[0], err
}

// reserveChildren validates admission and registers the entire batch under one
// lock, so another Spawn cannot consume capacity between check and registration.
// CurrentDepth is the immutable depth of this manager's parent, not a counter
// for concurrently running siblings.
func (m *Manager) reserveChildren(ctx context.Context, tasks []Task) ([]*ChildRef, []context.Context, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if m.cfg.CurrentDepth >= m.cfg.MaxDepth {
		return nil, nil, fmt.Errorf("depth limit reached (depth=%d, max=%d)", m.cfg.CurrentDepth, m.cfg.MaxDepth)
	}
	active := len(m.children) + m.runningAutonomousCount()
	if active+len(tasks) > m.cfg.MaxChildren {
		return nil, nil, fmt.Errorf("too many concurrent children: %d active + %d requested exceed limit of %d", active, len(tasks), m.cfg.MaxChildren)
	}
	refs := make([]*ChildRef, len(tasks))
	contexts := make([]context.Context, len(tasks))
	for i, task := range tasks {
		childCtx, cancel := context.WithTimeout(ctx, m.cfg.ChildTimeout)
		ref := &ChildRef{ID: "child-" + uuid.New().String(), Goal: task.Goal(), Model: task.Model(), Status: "running", StartedAt: time.Now(), Cancel: cancel, Depth: m.cfg.CurrentDepth + 1, Role: RoleWorker}
		refs[i] = ref
		contexts[i] = childCtx
		m.children[ref.ID] = ref
	}
	if m.communication != nil {
		records := make([]WorkerRecord, len(refs))
		for i, ref := range refs {
			name := "worker-" + strings.TrimPrefix(ref.ID, "child-")[:8]
			previous := ""
			if named, ok := tasks[i].(interface {
				WorkerLabel() string
				PreviousWorker() string
			}); ok {
				if named.WorkerLabel() != "" {
					name = named.WorkerLabel()
				}
				previous = named.PreviousWorker()
			}
			ref.Name = name
			records[i] = WorkerRecord{ID: ref.ID, Name: name, Goal: ref.Goal, Model: ref.Model, Tools: append([]string(nil), tasks[i].Toolset()...), FollowupOf: previous}
		}
		if err := m.communication.RegisterBatch(ctx, records); err != nil {
			for _, ref := range refs {
				ref.Cancel()
				delete(m.children, ref.ID)
			}
			return nil, nil, err
		}
	}
	return refs, contexts, nil
}

// runChild guarantees cleanup and truthful failure reporting even if a runner
// panics or violates its result contract. Partial measured usage is preserved.
func (m *Manager) runChild(ctx context.Context, task Task, index int, ref *ChildRef) (result *Result, runErr error) {
	start := time.Now()
	reason := "worker_error"
	defer func() {
		if recovered := recover(); recovered != nil {
			runErr = fmt.Errorf("subagent: worker panicked: %v", recovered)
		}
		if result == nil {
			result = &Result{}
			if runErr == nil {
				runErr = fmt.Errorf("subagent: worker returned no result")
			}
		}
		if runErr == nil && ctx.Err() != nil {
			runErr = ctx.Err()
		}
		if runErr == nil && result.Status != "completed" {
			runErr = fmt.Errorf("subagent: worker ended with status %q: %s", result.Status, result.Error)
		}
		if runErr != nil {
			result.Status = "error"
			result.ExitReason = reason
			if errors.Is(runErr, context.DeadlineExceeded) {
				result.Status = "timeout"
				result.ExitReason = "timeout"
			}
			if errors.Is(runErr, context.Canceled) {
				result.Status = "interrupted"
				result.ExitReason = "interrupted"
			}
			result.Error = runErr.Error()
			result.Summary = "Task failed: " + runErr.Error()
		}
		result.TaskIndex = index
		result.DurationMs = time.Since(start).Milliseconds()
		result.WorkerID = ref.ID
		if store := m.CommunicationStore(); store != nil {
			finishCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			err := store.Finish(finishCtx, ref.ID, result)
			cancel()
			if err != nil {
				runErr = errors.Join(runErr, fmt.Errorf("subagent: persist worker result: %w", err))
				result.Status = "error"
				result.Error = runErr.Error()
			}
		}
		ref.Cancel()
		m.mu.Lock()
		delete(m.children, ref.ID)
		m.mu.Unlock()
		m.costTracker.AddChild(result)
	}()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.RLock()
	factory, router := m.workerFactory, m.router
	m.mu.RUnlock()
	if factory == nil && len(task.Toolset()) > 0 {
		reason = "unsupported_capability"
		return nil, fmt.Errorf("%w: text-only worker cannot execute requested tools %v; configure a tool-capable contract driver", ErrUnsupportedCapability, task.Toolset())
	}
	model, provider := task.Model(), ""
	if router != nil && model == "" {
		if mid, prov, ok := router.RouteTask(ctx, task.Goal(), task.Context()); ok {
			model, provider = mid, prov
		}
	}
	cfg := RealWorkerConfig{WorkerID: ref.ID, Tools: append([]string(nil), task.Toolset()...), Goal: task.Goal(), Context: task.Context(), MaxSteps: task.MaxIterations(), Timeout: m.cfg.ChildTimeout, Model: model, Provider: provider, TaskIndex: index}
	var runner WorkerRunner
	var err error
	if factory != nil {
		runner, err = factory(cfg)
	} else {
		runner, err = NewRealWorker(cfg)
	}
	if err != nil {
		if errors.Is(err, ErrUnsupportedCapability) {
			reason = "unsupported_capability"
			return nil, err
		}
		reason = "worker_unavailable"
		return nil, fmt.Errorf("subagent: initialize worker: %w; configure a supported provider and its credentials", err)
	}
	if runner == nil {
		reason = "worker_unavailable"
		return nil, fmt.Errorf("subagent: worker factory returned no runner")
	}
	return runner.Run(ctx)
}

// validateTasks checks the whole request before reserving capacity or starting
// earlier batches. A typed nil task is still nil, despite its interface value.
func validateTasks(tasks []Task) error {
	for i, task := range tasks {
		if task == nil || (reflect.ValueOf(task).Kind() == reflect.Pointer && reflect.ValueOf(task).IsNil()) {
			return fmt.Errorf("task %d validation: task is nil", i)
		}
		if err := task.Validate(); err != nil {
			return fmt.Errorf("task %d validation: %w", i, err)
		}
	}
	return nil
}

// SpawnBatch validates all tasks before dispatch and reserves capacity for the
// whole batch. Independent siblings finish even if one fails. Results retain
// their input indices; the returned error joins every indexed child failure.
func (m *Manager) SpawnBatch(ctx context.Context, tasks []Task) ([]*Result, error) {
	if err := validateTasks(tasks); err != nil {
		return nil, err
	}
	if len(tasks) == 0 {
		return []*Result{}, nil
	}
	refs, contexts, err := m.reserveChildren(ctx, tasks)
	if err != nil {
		return nil, err
	}
	results := make([]*Result, len(tasks))
	failures := make([]error, len(tasks))
	var wg sync.WaitGroup
	for i, task := range tasks {
		wg.Go(func() {
			result, err := m.runChild(contexts[i], task, i, refs[i])
			results[i] = result
			if err != nil {
				failures[i] = fmt.Errorf("task %d: %w", i, err)
			}
		})
	}
	wg.Wait()
	return results, errors.Join(failures...)
}

// ActiveCount returns the number of currently running children (Spawn path).
func (m *Manager) ActiveCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.children)
}

// runningAutonomousCount counts in-flight contracts. The caller must hold m.mu
// while iterating the map; checking a done channel never needs to release it.
func (m *Manager) runningAutonomousCount() int {
	count := 0
	for _, entry := range m.autonomous {
		select {
		case <-entry.done:
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
		return min(m.cfg.MaxTasksPerChild, m.cfg.MaxChildren)
	}
	return m.cfg.MaxChildren
}

// SpawnBatchAutoSplit is like SpawnBatch but automatically splits oversized
// task lists into sequential batches. When len(tasks) > batchSize(), tasks are
// sliced into batches of batchSize(), each run via SpawnBatch in order, and
// results are concatenated. This prevents single sub-agents from hitting
// max_iterations when the parent dumps 9+ tasks on them.
func (m *Manager) SpawnBatchAutoSplit(ctx context.Context, tasks []Task) ([]*Result, error) {
	if err := validateTasks(tasks); err != nil {
		return nil, err
	}
	bs := m.batchSize()
	if len(tasks) <= bs {
		return m.SpawnBatch(ctx, tasks)
	}

	var all []*Result
	for start := 0; start < len(tasks); start += bs {
		end := min(start+bs, len(tasks))
		batch := tasks[start:end]
		results, err := m.SpawnBatch(ctx, batch)
		for i, result := range results {
			if result != nil {
				result.TaskIndex = start + i
			}
		}
		all = append(all, results...)
		if err != nil {
			return all, fmt.Errorf("batch [%d-%d]: %w", start+1, end, err)
		}
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
		task := GenericTask{GoalStr: goal, ContextStr: contextStr, ToolsetVal: m.DefaultToolset()}
		result, err := m.Spawn(ctx, task)
		if result == nil {
			return nil, err
		}
		return []*Result{result}, err
	}

	tasks := make([]Task, len(items))
	for i, desc := range items {
		tasks[i] = GenericTask{
			GoalStr:    desc,
			ContextStr: contextStr,
			ToolsetVal: m.DefaultToolset(),
		}
	}
	return m.SpawnBatchAutoSplit(ctx, tasks)
}

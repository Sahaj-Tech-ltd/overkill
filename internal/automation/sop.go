package automation

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Degraded workflows cannot execute until their persistence failure is resolved.
// A recovered running step has an unknown outcome and requires reconciliation;
// it must never be replayed automatically.
const SOPStatusDegraded SOPStatus = "degraded"

var ErrSOPPersistence = errors.New("automation: SOP persistence failed")
var ErrSOPUncertain = errors.New("automation: SOP step outcome requires reconciliation")

type SOP struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Mode        SOPMode           `json:"mode"`
	Steps       []Step            `json:"steps"`
	Status      SOPStatus         `json:"status"`
	CurrentStep int               `json:"current_step"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
	Metadata    map[string]string `json:"metadata"`
	// PersistenceError describes an in-memory degraded view, not a durable receipt.
	PersistenceError string `json:"persistence_error,omitempty"`
}

type SOPStore interface {
	SaveSOP(sop *SOP) error
	LoadSOPs() ([]SOP, error)
	DeleteSOP(id string) error
}

type SOPEngine struct {
	mu      sync.RWMutex
	sops    map[string]*SOP
	store   SOPStore
	execute func(action string) (string, error)
	running map[string]bool
	// pending contains known outcomes that can safely be persisted again without
	// repeating execution. It is deliberately not reconstructed from running steps.
	pending map[string]*SOP
	loadErr error
}

func NewSOPEngine(store SOPStore, executor func(action string) (string, error)) *SOPEngine {
	e := &SOPEngine{sops: map[string]*SOP{}, store: store, execute: executor, running: map[string]bool{}, pending: map[string]*SOP{}}
	if store != nil {
		saved, err := store.LoadSOPs()
		if err != nil {
			e.loadErr = fmt.Errorf("%w: load SOPs: %w", ErrSOPPersistence, err)
			return e
		}
		for i := range saved {
			sop := cloneSOP(&saved[i])
			for _, step := range sop.Steps {
				if step.Status == StepRunning {
					sop.Status = SOPStatusDegraded
					sop.PersistenceError = ErrSOPUncertain.Error()
					break
				}
			}
			e.sops[sop.ID] = sop
		}
	}
	return e
}

func cloneSOP(sop *SOP) *SOP {
	cp := *sop
	cp.Steps = append([]Step(nil), sop.Steps...)
	cp.Metadata = map[string]string{}
	for k, v := range sop.Metadata {
		cp.Metadata[k] = v
	}
	return &cp
}

// commitLocked persists an isolated snapshot before publishing it. Even stores
// that retain the supplied pointer cannot mutate the engine's acknowledged state.
func (e *SOPEngine) commitLocked(next *SOP) error {
	next = cloneSOP(next)
	next.UpdatedAt = time.Now().UTC()
	if e.store != nil {
		if err := e.store.SaveSOP(next); err != nil {
			return fmt.Errorf("%w: SOP %s: %w", ErrSOPPersistence, next.ID, err)
		}
	}
	e.sops[next.ID] = cloneSOP(next)
	return nil
}

func (e *SOPEngine) Create(sop *SOP) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.loadErr != nil {
		return e.loadErr
	}
	if sop == nil {
		return fmt.Errorf("automation: create SOP: %w", ErrInvalidState)
	}
	if _, exists := e.sops[sop.ID]; exists {
		return fmt.Errorf("automation: create SOP %s: %w", sop.ID, ErrAlreadyExists)
	}
	next := cloneSOP(sop)
	next.CreatedAt = time.Now().UTC()
	next.Status = SOPStatusActive
	next.CurrentStep = 0
	next.PersistenceError = ""
	for i := range next.Steps {
		if next.Steps[i].Status == "" {
			next.Steps[i].Status = StepPending
		}
	}
	return e.commitLocked(next)
}

func (e *SOPEngine) Execute(ctx context.Context, id string) error {
	e.mu.Lock()
	if err := e.beginLocked(id); err != nil {
		e.mu.Unlock()
		return err
	}
	e.mu.Unlock()
	defer e.endExecution(id)
	return e.executeSteps(ctx, id)
}

func (e *SOPEngine) beginLocked(id string) error {
	if e.loadErr != nil {
		return e.loadErr
	}
	sop, exists := e.sops[id]
	if !exists {
		return fmt.Errorf("automation: execute SOP %s: %w", id, ErrNotFound)
	}
	if e.running[id] || sop.Status != SOPStatusActive {
		return fmt.Errorf("automation: execute SOP %s: status %s: %w", id, sop.Status, ErrInvalidState)
	}
	if len(sop.Steps) == 0 {
		return fmt.Errorf("automation: execute SOP %s: %w", id, ErrNoSteps)
	}
	for _, step := range sop.Steps {
		if step.Status == StepRunning {
			return fmt.Errorf("SOP %s: %w", id, ErrSOPUncertain)
		}
	}
	e.running[id] = true
	return nil
}
func (e *SOPEngine) endExecution(id string) { e.mu.Lock(); delete(e.running, id); e.mu.Unlock() }

func (e *SOPEngine) executeSteps(ctx context.Context, id string) error {
	indices := e.stepOrder(id)
	var prevOutput string
	for _, idx := range indices {
		e.mu.Lock()
		sop := e.sops[id]
		if sop.Status == SOPStatusCancelled || sop.Status == SOPStatusPaused {
			e.mu.Unlock()
			return nil
		}
		if err := ctx.Err(); err != nil {
			next := cloneSOP(sop)
			next.Status = SOPStatusFailed
			persistErr := e.commitLocked(next)
			e.mu.Unlock()
			return errors.Join(fmt.Errorf("automation: execute SOP %s: %w", id, err), persistErr)
		}
		step := sop.Steps[idx]
		if step.Status == StepDone {
			prevOutput = step.Output
			e.mu.Unlock()
			continue
		}
		if step.Status == StepSkipped {
			e.mu.Unlock()
			continue
		}
		if step.Status == StepRunning {
			e.mu.Unlock()
			return fmt.Errorf("SOP %s step %s: %w", id, step.ID, ErrSOPUncertain)
		}
		next := cloneSOP(sop)
		next.CurrentStep = idx
		needsApproval := (sop.Mode == ModeSupervised || sop.Mode == ModeStepByStep) && step.RequiresApproval
		if sop.Mode == ModeStepByStep {
			needsApproval = true
		}
		if needsApproval {
			next.Steps[idx].Status = StepWaiting
			err := e.commitLocked(next)
			e.mu.Unlock()
			if err != nil {
				return err
			}
			return fmt.Errorf("automation: SOP %s step %s: %w", id, step.ID, ErrStepWaiting)
		}
		next.Steps[idx].Status = StepRunning
		// Write-ahead checkpoint: no external action before durable intent. A crash
		// after this boundary is uncertain, not permission to repeat the action.
		if err := e.commitLocked(next); err != nil {
			e.mu.Unlock()
			return err
		}
		action := step.Action
		if sop.Mode == ModeDeterministic && idx > 0 && prevOutput != "" {
			action = prevOutput
		}
		e.mu.Unlock()

		output, executeErr := e.execute(action)

		e.mu.Lock()
		next = cloneSOP(e.sops[id]) // preserve a concurrent durable Pause/Cancel
		if executeErr != nil {
			next.Steps[idx].Status = StepFailed
			next.Steps[idx].Error = executeErr.Error()
			if next.Status == SOPStatusActive {
				next.Status = SOPStatusFailed
			}
		} else {
			next.Steps[idx].Status = StepDone
			next.Steps[idx].Output = output
			next.Steps[idx].Error = ""
			prevOutput = output
		}
		err := e.commitLocked(next)
		if err != nil {
			e.degradeLocked(next, err)
		}
		e.mu.Unlock()
		if executeErr != nil {
			return errors.Join(fmt.Errorf("automation: SOP %s step %s failed: %w", id, step.ID, executeErr), err)
		}
		if err != nil {
			return err
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	sop := e.sops[id]
	if sop.Status == SOPStatusCancelled || sop.Status == SOPStatusPaused {
		return nil
	}
	next := cloneSOP(sop)
	next.Status = SOPStatusCompleted
	if err := e.commitLocked(next); err != nil {
		e.degradeLocked(next, err)
		return err
	}
	return nil
}

func (e *SOPEngine) degradeLocked(outcome *SOP, err error) {
	e.pending[outcome.ID] = cloneSOP(outcome)
	view := cloneSOP(outcome)
	view.Status = SOPStatusDegraded
	view.PersistenceError = err.Error()
	e.sops[outcome.ID] = view
}

// RetryPersistence retries only a known in-process outcome write. It never
// invokes an executor. After a restart, StepRunning is intentionally uncertain
// and cannot use this path: an operator must reconcile the external effect.
func (e *SOPEngine) RetryPersistence(id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.loadErr != nil {
		return e.loadErr
	}
	if e.running[id] {
		return fmt.Errorf("SOP %s: %w", id, ErrInvalidState)
	}
	next, ok := e.pending[id]
	if !ok {
		if _, exists := e.sops[id]; !exists {
			return ErrNotFound
		}
		return ErrSOPUncertain
	}
	if err := e.commitLocked(next); err != nil {
		e.degradeLocked(next, err)
		return err
	}
	delete(e.pending, id)
	return nil
}

func (e *SOPEngine) stepOrder(id string) []int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	sop := e.sops[id]
	indices := make([]int, len(sop.Steps))
	for i := range indices {
		indices[i] = i
	}
	if sop.Mode == ModePriority {
		sort.SliceStable(indices, func(a, b int) bool { return sop.Steps[indices[a]].Priority < sop.Steps[indices[b]].Priority })
	}
	return indices
}

func (e *SOPEngine) ApproveStep(id, stepID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.loadErr != nil {
		return e.loadErr
	}
	sop, ok := e.sops[id]
	if !ok {
		return fmt.Errorf("automation: approve SOP %s: %w", id, ErrNotFound)
	}
	if sop.Status != SOPStatusActive || e.running[id] {
		return fmt.Errorf("automation: approve SOP %s: %w", id, ErrInvalidState)
	}
	next := cloneSOP(sop)
	for i := range next.Steps {
		if next.Steps[i].ID == stepID {
			if next.Steps[i].Status != StepWaiting {
				return fmt.Errorf("automation: step %s: %w", stepID, ErrInvalidState)
			}
			next.Steps[i].Status = StepPending
			next.Steps[i].RequiresApproval = false
			return e.commitLocked(next)
		}
	}
	return fmt.Errorf("automation: step %s in SOP %s: %w", stepID, id, ErrNotFound)
}

func (e *SOPEngine) Cancel(id string) error { return e.transition(id, SOPStatusCancelled, false) }
func (e *SOPEngine) Pause(id string) error  { return e.transition(id, SOPStatusPaused, true) }
func (e *SOPEngine) transition(id string, status SOPStatus, requireActive bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.loadErr != nil {
		return e.loadErr
	}
	sop, ok := e.sops[id]
	if !ok {
		return fmt.Errorf("automation: SOP %s: %w", id, ErrNotFound)
	}
	if sop.Status == SOPStatusDegraded || (requireActive && sop.Status != SOPStatusActive) {
		return fmt.Errorf("automation: SOP %s: %w", id, ErrInvalidState)
	}
	next := cloneSOP(sop)
	next.Status = status
	return e.commitLocked(next)
}

func (e *SOPEngine) Resume(ctx context.Context, id string) error {
	e.mu.Lock()
	if e.loadErr != nil {
		e.mu.Unlock()
		return e.loadErr
	}
	sop, ok := e.sops[id]
	if !ok {
		e.mu.Unlock()
		return fmt.Errorf("automation: resume SOP %s: %w", id, ErrNotFound)
	}
	if sop.Status != SOPStatusPaused || e.running[id] {
		e.mu.Unlock()
		return fmt.Errorf("automation: resume SOP %s: %w", id, ErrInvalidState)
	}
	for _, step := range sop.Steps {
		if step.Status == StepRunning {
			e.mu.Unlock()
			return fmt.Errorf("SOP %s: %w", id, ErrSOPUncertain)
		}
	}
	next := cloneSOP(sop)
	next.Status = SOPStatusActive
	if err := e.commitLocked(next); err != nil {
		e.mu.Unlock()
		return err
	}
	if err := e.beginLocked(id); err != nil {
		e.mu.Unlock()
		return err
	}
	e.mu.Unlock()
	defer e.endExecution(id)
	return e.executeSteps(ctx, id)
}

func (e *SOPEngine) Get(id string) (*SOP, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	sop, ok := e.sops[id]
	if !ok {
		return nil, false
	}
	return cloneSOP(sop), true
}
func (e *SOPEngine) List() []*SOP {
	e.mu.RLock()
	defer e.mu.RUnlock()
	result := make([]*SOP, 0, len(e.sops))
	for _, sop := range e.sops {
		result = append(result, cloneSOP(sop))
	}
	return result
}

package automation

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"
)

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
}

func NewSOPEngine(store SOPStore, executor func(action string) (string, error)) *SOPEngine {
	return &SOPEngine{
		sops:    make(map[string]*SOP),
		store:   store,
		execute: executor,
	}
}

func (e *SOPEngine) Create(sop *SOP) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if _, exists := e.sops[sop.ID]; exists {
		return fmt.Errorf("automation: create SOP %s: %w", sop.ID, ErrAlreadyExists)
	}

	now := time.Now().UTC()
	sop.CreatedAt = now
	sop.UpdatedAt = now
	sop.Status = SOPStatusActive
	sop.CurrentStep = 0

	for i := range sop.Steps {
		if sop.Steps[i].Status == "" {
			sop.Steps[i].Status = StepPending
		}
	}

	cp := *sop
	if cp.Metadata == nil {
		cp.Metadata = make(map[string]string)
	}
	cp.Steps = make([]Step, len(sop.Steps))
	copy(cp.Steps, sop.Steps)

	e.sops[sop.ID] = &cp

	if e.store != nil {
		if err := e.store.SaveSOP(&cp); err != nil {
			return fmt.Errorf("automation: persist SOP %s: %w", sop.ID, err)
		}
	}

	return nil
}

func (e *SOPEngine) Execute(ctx context.Context, id string) error {
	e.mu.Lock()
	sop, exists := e.sops[id]
	if !exists {
		e.mu.Unlock()
		return fmt.Errorf("automation: execute SOP %s: %w", id, ErrNotFound)
	}

	if sop.Status != SOPStatusActive {
		e.mu.Unlock()
		return fmt.Errorf("automation: execute SOP %s: status %s: %w", id, sop.Status, ErrInvalidState)
	}

	if len(sop.Steps) == 0 {
		e.mu.Unlock()
		return fmt.Errorf("automation: execute SOP %s: %w", id, ErrNoSteps)
	}

	e.mu.Unlock()

	return e.executeSteps(ctx, id)
}

func (e *SOPEngine) executeSteps(ctx context.Context, id string) error {
	indices := e.stepOrder(id)

	// Seed prevOutput from the last already-done step so
	// ModeDeterministic preserves the step-N output → step-N+1
	// input chain across Pause/Resume. Without this, every Resume
	// silently fed "" into the first remaining deterministic step
	// and the chain broke.
	var prevOutput string
	e.mu.RLock()
	if sop, ok := e.sops[id]; ok {
		for _, idx := range indices {
			if idx >= len(sop.Steps) {
				continue
			}
			if sop.Steps[idx].Status == StepDone {
				prevOutput = sop.Steps[idx].Output
			}
		}
	}
	e.mu.RUnlock()

	for _, idx := range indices {
		select {
		case <-ctx.Done():
			e.mu.Lock()
			sop := e.sops[id]
			sop.Status = SOPStatusFailed
			sop.UpdatedAt = time.Now().UTC()
			e.mu.Unlock()
			return fmt.Errorf("automation: execute SOP %s: %w", id, ctx.Err())
		default:
		}

		e.mu.Lock()
		sop := e.sops[id]

		if sop.Status == SOPStatusCancelled || sop.Status == SOPStatusPaused {
			e.mu.Unlock()
			return nil
		}

		step := &sop.Steps[idx]
		// Skip steps already completed (Resume path) or explicitly
		// skipped. Otherwise we re-run done work, defeating the
		// resumable-from-last-completed-step guarantee.
		if step.Status == StepDone || step.Status == StepSkipped {
			e.mu.Unlock()
			continue
		}
		step.Status = StepRunning
		sop.CurrentStep = idx
		sop.UpdatedAt = time.Now().UTC()

		needsApproval := (sop.Mode == ModeSupervised || sop.Mode == ModeStepByStep) && step.RequiresApproval
		if sop.Mode == ModeStepByStep {
			needsApproval = true
		}

		if needsApproval {
			step.Status = StepWaiting
			if e.store != nil {
				_ = e.store.SaveSOP(sop)
			}
			e.mu.Unlock()
			return fmt.Errorf("automation: SOP %s step %s: %w", id, step.ID, ErrStepWaiting)
		}

		action := step.Action
		if sop.Mode == ModeDeterministic && idx > 0 && prevOutput != "" {
			action = prevOutput
		}

		e.mu.Unlock()

		output, err := e.execute(action)

		e.mu.Lock()
		sop = e.sops[id]
		step = &sop.Steps[idx]

		if err != nil {
			step.Status = StepFailed
			step.Error = err.Error()
			sop.Status = SOPStatusFailed
			sop.UpdatedAt = time.Now().UTC()
			if e.store != nil {
				_ = e.store.SaveSOP(sop)
			}
			e.mu.Unlock()
			return fmt.Errorf("automation: SOP %s step %s failed: %w", id, step.ID, err)
		}

		step.Status = StepDone
		step.Output = output
		prevOutput = output
		sop.UpdatedAt = time.Now().UTC()
		if e.store != nil {
			_ = e.store.SaveSOP(sop)
		}
		e.mu.Unlock()
	}

	e.mu.Lock()
	sop := e.sops[id]
	sop.Status = SOPStatusCompleted
	sop.UpdatedAt = time.Now().UTC()
	if e.store != nil {
		_ = e.store.SaveSOP(sop)
	}
	e.mu.Unlock()

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
		sort.Slice(indices, func(a, b int) bool {
			return sop.Steps[indices[a]].Priority < sop.Steps[indices[b]].Priority
		})
	}

	return indices
}

func (e *SOPEngine) ApproveStep(id string, stepID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	sop, exists := e.sops[id]
	if !exists {
		return fmt.Errorf("automation: approve SOP %s step %s: %w", id, stepID, ErrNotFound)
	}

	if sop.Status != SOPStatusActive {
		return fmt.Errorf("automation: approve SOP %s: status %s: %w", id, sop.Status, ErrInvalidState)
	}

	for i := range sop.Steps {
		if sop.Steps[i].ID == stepID {
			if sop.Steps[i].Status != StepWaiting {
				return fmt.Errorf("automation: step %s status %s: %w", stepID, sop.Steps[i].Status, ErrInvalidState)
			}
			sop.Steps[i].Status = StepPending
			sop.Steps[i].RequiresApproval = false
			sop.UpdatedAt = time.Now().UTC()
			if e.store != nil {
				_ = e.store.SaveSOP(sop)
			}
			return nil
		}
	}

	return fmt.Errorf("automation: step %s in SOP %s: %w", stepID, id, ErrNotFound)
}

func (e *SOPEngine) Cancel(id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	sop, exists := e.sops[id]
	if !exists {
		return fmt.Errorf("automation: cancel SOP %s: %w", id, ErrNotFound)
	}

	sop.Status = SOPStatusCancelled
	sop.UpdatedAt = time.Now().UTC()
	if e.store != nil {
		_ = e.store.SaveSOP(sop)
	}
	return nil
}

func (e *SOPEngine) Pause(id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	sop, exists := e.sops[id]
	if !exists {
		return fmt.Errorf("automation: pause SOP %s: %w", id, ErrNotFound)
	}

	if sop.Status != SOPStatusActive {
		return fmt.Errorf("automation: pause SOP %s: status %s: %w", id, sop.Status, ErrInvalidState)
	}

	sop.Status = SOPStatusPaused
	sop.UpdatedAt = time.Now().UTC()
	if e.store != nil {
		_ = e.store.SaveSOP(sop)
	}
	return nil
}

func (e *SOPEngine) Resume(ctx context.Context, id string) error {
	e.mu.Lock()

	sop, exists := e.sops[id]
	if !exists {
		e.mu.Unlock()
		return fmt.Errorf("automation: resume SOP %s: %w", id, ErrNotFound)
	}

	if sop.Status != SOPStatusPaused {
		e.mu.Unlock()
		return fmt.Errorf("automation: resume SOP %s: status %s: %w", id, sop.Status, ErrInvalidState)
	}

	sop.Status = SOPStatusActive
	sop.UpdatedAt = time.Now().UTC()
	if e.store != nil {
		_ = e.store.SaveSOP(sop)
	}
	e.mu.Unlock()

	return e.executeSteps(ctx, id)
}

func (e *SOPEngine) Get(id string) (*SOP, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	sop, exists := e.sops[id]
	if !exists {
		return nil, false
	}
	cp := *sop
	cp.Steps = make([]Step, len(sop.Steps))
	copy(cp.Steps, sop.Steps)
	return &cp, true
}

func (e *SOPEngine) List() []*SOP {
	e.mu.RLock()
	defer e.mu.RUnlock()

	result := make([]*SOP, 0, len(e.sops))
	for _, sop := range e.sops {
		cp := *sop
		cp.Steps = make([]Step, len(sop.Steps))
		copy(cp.Steps, sop.Steps)
		result = append(result, &cp)
	}
	return result
}

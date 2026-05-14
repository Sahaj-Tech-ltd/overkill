package automation

import (
	"fmt"
	"sync"
	"time"
)

type Routine struct {
	ID        string        `json:"id"`
	Name      string        `json:"name"`
	Trigger   string        `json:"trigger"`
	Action    string        `json:"action"`
	Cooldown  time.Duration `json:"cooldown"`
	Enabled   bool          `json:"enabled"`
	LastFired time.Time     `json:"last_fired"`
	FireCount int           `json:"fire_count"`
}

type RoutineEngine struct {
	mu       sync.RWMutex
	routines map[string]*Routine
	fire     func(action string) (string, error)
	// store persists mutations so a daemon restart preserves
	// registered routines AND their cooldown state. Optional;
	// nil disables persistence (test ergonomics + memory-only
	// embeddings).
	store RoutineStore
}

func NewRoutineEngine(fire func(action string) (string, error)) *RoutineEngine {
	return &RoutineEngine{
		routines: make(map[string]*Routine),
		fire:     fire,
	}
}

// NewRoutineEngineWithStore wires the engine to a durable store and
// replays every persisted routine into the in-memory map. The
// store is consulted on every mutation (Register / Unregister /
// Enable / Disable / HandleEvent's LastFired update).
//
// Returns an error only on the initial Load; persistence failures
// during operation are surfaced from the individual mutator
// methods.
func NewRoutineEngineWithStore(fire func(action string) (string, error), store RoutineStore) (*RoutineEngine, error) {
	e := NewRoutineEngine(fire)
	e.store = store
	if store == nil {
		return e, nil
	}
	loaded, err := store.Load()
	if err != nil {
		return e, fmt.Errorf("automation: load routines: %w", err)
	}
	for _, r := range loaded {
		cp := *r
		e.routines[r.ID] = &cp
	}
	return e, nil
}

func (e *RoutineEngine) Register(routine *Routine) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if _, exists := e.routines[routine.ID]; exists {
		return fmt.Errorf("automation: register routine %s: %w", routine.ID, ErrAlreadyExists)
	}

	cp := *routine
	e.routines[routine.ID] = &cp
	return e.persistLocked(&cp)
}

func (e *RoutineEngine) Unregister(id string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()

	_, exists := e.routines[id]
	if !exists {
		return false
	}
	delete(e.routines, id)
	if e.store != nil {
		// Persistence failure here is best-effort — the routine
		// is gone from memory either way. Log via the engine's
		// own error channel if we ever add one.
		_ = e.store.Delete(id)
	}
	return true
}

func (e *RoutineEngine) HandleEvent(trigger string) (bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	var fired bool
	now := time.Now()

	for _, r := range e.routines {
		if !r.Enabled {
			continue
		}
		if r.Trigger != trigger {
			continue
		}
		if !r.LastFired.IsZero() && now.Sub(r.LastFired) < r.Cooldown {
			continue
		}

		_, err := e.fire(r.Action)
		if err != nil {
			return fired, fmt.Errorf("automation: routine %s fired: %w", r.ID, err)
		}

		r.LastFired = now
		r.FireCount++
		fired = true
		if err := e.persistLocked(r); err != nil {
			// Persisting cooldown state mid-event is best-effort.
			// Surface via the error return so a daemon-level
			// handler can log it, but don't drop the firing.
			return fired, fmt.Errorf("automation: routine %s persist cooldown: %w", r.ID, err)
		}
	}

	return fired, nil
}

func (e *RoutineEngine) List() []*Routine {
	e.mu.RLock()
	defer e.mu.RUnlock()

	result := make([]*Routine, 0, len(e.routines))
	for _, r := range e.routines {
		cp := *r
		result = append(result, &cp)
	}
	return result
}

func (e *RoutineEngine) Enable(id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	r, exists := e.routines[id]
	if !exists {
		return fmt.Errorf("automation: enable routine %s: %w", id, ErrNotFound)
	}
	r.Enabled = true
	return e.persistLocked(r)
}

func (e *RoutineEngine) Disable(id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	r, exists := e.routines[id]
	if !exists {
		return fmt.Errorf("automation: disable routine %s: %w", id, ErrNotFound)
	}
	r.Enabled = false
	return e.persistLocked(r)
}

// persistLocked saves a routine via the store, if wired. Caller MUST
// hold e.mu. nil store is a no-op so tests and in-memory uses don't
// have to wire one.
func (e *RoutineEngine) persistLocked(r *Routine) error {
	if e.store == nil {
		return nil
	}
	cp := *r
	return e.store.Save(&cp)
}

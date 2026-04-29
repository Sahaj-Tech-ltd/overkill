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
}

func NewRoutineEngine(fire func(action string) (string, error)) *RoutineEngine {
	return &RoutineEngine{
		routines: make(map[string]*Routine),
		fire:     fire,
	}
}

func (e *RoutineEngine) Register(routine *Routine) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if _, exists := e.routines[routine.ID]; exists {
		return fmt.Errorf("automation: register routine %s: %w", routine.ID, ErrAlreadyExists)
	}

	cp := *routine
	e.routines[routine.ID] = &cp
	return nil
}

func (e *RoutineEngine) Unregister(id string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()

	_, exists := e.routines[id]
	if !exists {
		return false
	}
	delete(e.routines, id)
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
	return nil
}

func (e *RoutineEngine) Disable(id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	r, exists := e.routines[id]
	if !exists {
		return fmt.Errorf("automation: disable routine %s: %w", id, ErrNotFound)
	}
	r.Enabled = false
	return nil
}

package hooks

import (
	"context"
	"fmt"
	"log"
	"sort"
	"sync"
)

type Registry struct {
	mu    sync.RWMutex
	hooks map[HookPoint][]Hook
	names map[string]bool
}

func NewRegistry() *Registry {
	return &Registry{
		hooks: make(map[HookPoint][]Hook),
		names: make(map[string]bool),
	}
}

func (r *Registry) Register(hook Hook) error {
	if hook.Name == "" {
		return fmt.Errorf("hooks: hook name is required")
	}
	if hook.Fn == nil {
		return fmt.Errorf("hooks: hook function is required")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if r.names[hook.Name] {
		return fmt.Errorf("hooks: hook %q already registered", hook.Name)
	}

	r.names[hook.Name] = true
	r.hooks[hook.Point] = append(r.hooks[hook.Point], hook)
	sort.SliceStable(r.hooks[hook.Point], func(i, j int) bool {
		return r.hooks[hook.Point][i].Priority < r.hooks[hook.Point][j].Priority
	})

	return nil
}

func (r *Registry) Unregister(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.names[name] {
		return false
	}

	for point, hooks := range r.hooks {
		for i, h := range hooks {
			if h.Name == name {
				r.hooks[point] = append(hooks[:i], hooks[i+1:]...)
				delete(r.names, name)
				return true
			}
		}
	}

	delete(r.names, name)
	return false
}

func (r *Registry) Fire(ctx context.Context, point HookPoint, event Event) (context.Context, error) {
	r.mu.RLock()
	hooks := make([]Hook, len(r.hooks[point]))
	copy(hooks, r.hooks[point])
	r.mu.RUnlock()

	if len(hooks) == 0 {
		return ctx, nil
	}

	var firstErr error
	for _, hook := range hooks {
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("hooks: panic in hook %q at %s: %v", hook.Name, point, r)
				}
			}()

			var err error
			ctx, err = hook.Fn(ctx, event)
			if err != nil {
				log.Printf("hooks: error in hook %q at %s: %v", hook.Name, point, err)
				if firstErr == nil {
					firstErr = fmt.Errorf("hooks: hook %q failed: %w", hook.Name, err)
				}
			}
		}()
	}

	return ctx, firstErr
}

func (r *Registry) List(point HookPoint) []Hook {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]Hook, len(r.hooks[point]))
	copy(result, r.hooks[point])
	return result
}

func (r *Registry) ListAll() map[HookPoint][]Hook {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make(map[HookPoint][]Hook, len(r.hooks))
	for point, hooks := range r.hooks {
		copied := make([]Hook, len(hooks))
		copy(copied, hooks)
		result[point] = copied
	}
	return result
}

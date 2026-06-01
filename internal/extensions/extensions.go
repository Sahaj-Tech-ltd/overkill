// Package extensions is a thin, read-mostly unification layer over the
// four existing extension surfaces in Overkill: skills, plugins, hooks,
// and MCP servers (Phase 1.5 #5).
//
// Goals (kept deliberately small):
//   - One inventory view: List() returns everything the user can toggle,
//     regardless of which backend owns it.
//   - One Enable/Disable verb: callers don't need to know which Registry
//     to talk to.
//   - One status check: Get(id) returns Enabled/Source/Description.
//
// Non-goals:
//   - Hot-reload coordination (each backend owns that — skills already
//     has fsnotify, plugins doesn't, MCP relies on server lifecycle).
//   - Cross-backend dependencies / capability negotiation.
//   - Replacing the four backends — we ADAPT them, not rewrite them.
//
// New backends register themselves with the Manager. The Manager has no
// knowledge of skill/plugin/hook semantics — it just delegates.
package extensions

import (
	"fmt"
	"log"
	"sort"
	"sync"
)

// Kind identifies which underlying system owns an extension.
type Kind string

const (
	KindSkill  Kind = "skill"
	KindPlugin Kind = "plugin"
	KindHook   Kind = "hook"
	KindMCP    Kind = "mcp"
)

// Extension is the unified record returned by Manager.List/Get.
// Backends translate their native representations into this struct.
type Extension struct {
	Kind        Kind   `json:"kind"`
	ID          string `json:"id"`          // unique within Kind
	Name        string `json:"name"`        // display name
	Description string `json:"description"` // one-liner
	Source      string `json:"source"`      // bundled | user | remote | mcp-server-name
	Enabled     bool   `json:"enabled"`
	// Metadata is free-form per-backend payload (version, tags, etc.).
	// Consumers (UI) shouldn't depend on specific keys — backend-specific
	// detail belongs in dedicated dialogs.
	Metadata map[string]string `json:"metadata,omitempty"`
}

// Backend is implemented by each of the four extension systems. Empty
// returns are fine (a not-yet-wired backend can ship as a stub).
type Backend interface {
	// Kind reports which extension family this backend serves.
	Kind() Kind
	// List returns the full inventory the backend knows about. Order
	// is unimportant — Manager sorts the merged view.
	List() ([]Extension, error)
	// Get returns a single extension by ID, or ErrNotFound. Added
	// in B138 to let Manager.Get do O(1) lookups instead of O(n)
	// scanning List() results.
	Get(id string) (*Extension, error)
	// Enable / Disable toggle an extension by its (Kind, ID). When the
	// backend doesn't support runtime toggling (e.g. hooks are
	// directory-based), it returns ErrUnsupported. Callers surface
	// that to the user instead of pretending the operation succeeded.
	Enable(id string) error
	Disable(id string) error
}

// ErrUnsupported is returned by Backend.Enable/Disable when the
// operation isn't supported for that backend (e.g. a directory-based
// extension that can't be toggled at runtime).
var ErrUnsupported = fmt.Errorf("extensions: operation unsupported by this backend")

// ErrNotFound is returned by Manager.Get / Enable / Disable when no
// backend recognises the requested ID.
var ErrNotFound = fmt.Errorf("extensions: not found")

// Manager fans out to the registered backends. Safe for concurrent use.
type Manager struct {
	mu       sync.RWMutex
	backends map[Kind]Backend
}

// NewManager returns an empty manager. Register backends with
// AddBackend before use.
func NewManager() *Manager {
	return &Manager{backends: make(map[Kind]Backend)}
}

// AddBackend installs a backend. Calling twice with the same Kind
// REPLACES the prior backend (last-wins) so the wire-up can be
// reordered without panic. Nil backend is a no-op.
func (m *Manager) AddBackend(b Backend) {
	if b == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.backends[b.Kind()]; exists {
		log.Printf("extensions: replacing existing backend for kind %q", b.Kind())
	}
	m.backends[b.Kind()] = b
}

// List returns every extension across every registered backend, sorted
// by (Kind, ID) for stable output. Backend errors surface as a single
// joined error — partial results are still returned so the UI can show
// what we DID find.
func (m *Manager) List() ([]Extension, error) {
	m.mu.RLock()
	backends := make([]Backend, 0, len(m.backends))
	for _, b := range m.backends {
		backends = append(backends, b)
	}
	m.mu.RUnlock()

	var all []Extension
	var firstErr error
	for _, b := range backends {
		entries, err := b.List()
		if err != nil && firstErr == nil {
			firstErr = fmt.Errorf("backend %s: %w", b.Kind(), err)
		}
		all = append(all, entries...)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].Kind != all[j].Kind {
			return all[i].Kind < all[j].Kind
		}
		return all[i].ID < all[j].ID
	})
	return all, firstErr
}

// Get returns the matching extension by (Kind, ID). When the backend
// implements Backend.Get, we use the O(1) lookup. Otherwise we fall
// back to scanning List() results (B138).
func (m *Manager) Get(kind Kind, id string) (*Extension, error) {
	m.mu.RLock()
	b, ok := m.backends[kind]
	m.mu.RUnlock()
	if !ok {
		return nil, ErrNotFound
	}
	// Prefer O(1) Get if the backend supports it.
	if ext, err := b.Get(id); err == nil {
		return ext, nil
	}
	// Fall back to O(n) List scan for backends that haven't
	// implemented Get yet.
	entries, err := b.List()
	if err != nil {
		return nil, err
	}
	for i := range entries {
		if entries[i].ID == id {
			cp := entries[i]
			return &cp, nil
		}
	}
	return nil, ErrNotFound
}

// Enable delegates to the backend for the given kind. ErrUnsupported
// bubbles up so the UI can surface "this kind of extension can't be
// toggled at runtime" instead of silently no-op'ing.
func (m *Manager) Enable(kind Kind, id string) error {
	b := m.backendFor(kind)
	if b == nil {
		return ErrNotFound
	}
	return b.Enable(id)
}

// Disable mirrors Enable.
func (m *Manager) Disable(kind Kind, id string) error {
	b := m.backendFor(kind)
	if b == nil {
		return ErrNotFound
	}
	return b.Disable(id)
}

func (m *Manager) backendFor(kind Kind) Backend {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.backends[kind]
}

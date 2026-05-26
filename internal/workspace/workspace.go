// Package workspace tracks a list of project directories the user works in,
// allowing fast switching between them from the TUI.
package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Workspace is a single project entry.
type Workspace struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Path     string    `json:"path"`
	LastUsed time.Time `json:"last_used"`
}

// Manager owns the workspace registry. The registry is a single JSON file at
// ~/.overkill/workspaces.json.
type Manager struct {
	mu        sync.RWMutex
	path      string
	items     []Workspace
	currentID string
}

// NewManager opens (or creates) a workspace registry. If path is empty, the
// default ~/.overkill/workspaces.json is used.
func NewManager(path string) (*Manager, error) {
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		path = filepath.Join(home, ".overkill", "workspaces.json")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	m := &Manager{path: path}
	if err := m.load(); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *Manager) load() error {
	data, err := os.ReadFile(m.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var blob struct {
		Workspaces []Workspace `json:"workspaces"`
		Current    string      `json:"current"`
	}
	if err := json.Unmarshal(data, &blob); err != nil {
		return err
	}
	m.items = blob.Workspaces
	m.currentID = blob.Current
	return nil
}

func (m *Manager) save() error {
	blob := struct {
		Workspaces []Workspace `json:"workspaces"`
		Current    string      `json:"current"`
	}{m.items, m.currentID}
	data, err := json.MarshalIndent(blob, "", "  ")
	if err != nil {
		return err
	}
	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, m.path)
}

func idFor(path string) string {
	h := sha256.Sum256([]byte(path))
	return hex.EncodeToString(h[:])[:12]
}

// Add registers a new workspace. If one with the same path already exists,
// the existing record is returned (with name updated if non-empty).
func (m *Manager) Add(path, name string) (*Workspace, error) {
	if path == "" {
		return nil, errors.New("workspace: path required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if name == "" {
		name = filepath.Base(abs)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, w := range m.items {
		if w.Path == abs {
			m.items[i].Name = name
			if err := m.save(); err != nil {
				return nil, err
			}
			out := m.items[i]
			return &out, nil
		}
	}
	w := Workspace{ID: idFor(abs), Name: name, Path: abs, LastUsed: time.Now().UTC()}
	m.items = append(m.items, w)
	if err := m.save(); err != nil {
		return nil, err
	}
	return &w, nil
}

// Remove deletes a workspace from the registry.
func (m *Manager) Remove(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := m.items[:0]
	for _, w := range m.items {
		if w.ID != id {
			out = append(out, w)
		}
	}
	m.items = out
	if m.currentID == id {
		m.currentID = ""
	}
	return m.save()
}

// List returns workspaces sorted by LastUsed descending.
func (m *Manager) List() []Workspace {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Workspace, len(m.items))
	copy(out, m.items)
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].LastUsed.After(out[j].LastUsed)
	})
	return out
}

// Switch makes the workspace with the given ID current. Updates LastUsed and
// chdir's to the workspace path. Equivalent to SwitchWith(id, nil).
func (m *Manager) Switch(id string) (*Workspace, error) {
	return m.SwitchWith(id, nil)
}

// SwitchWith is Switch + an optional callback fired after the chdir but
// before returning. The callback receives the resolved workspace; any error
// it returns is propagated. Callers use this to reopen per-workspace state
// (session store, file panels, …) atomically with the directory swap.
func (m *Manager) SwitchWith(id string, onSwitch func(Workspace) error) (*Workspace, error) {
	m.mu.Lock()
	for i, w := range m.items {
		if w.ID == id {
			m.items[i].LastUsed = time.Now().UTC()
			m.currentID = id
			ws := m.items[i]
			if err := m.save(); err != nil {
				m.mu.Unlock()
				return nil, err
			}
			m.mu.Unlock()
			if err := os.Chdir(ws.Path); err != nil {
				return nil, fmt.Errorf("workspace: chdir: %w", err)
			}
			if onSwitch != nil {
				if err := onSwitch(ws); err != nil {
					return &ws, fmt.Errorf("workspace: post-switch hook: %w", err)
				}
			}
			return &ws, nil
		}
	}
	m.mu.Unlock()
	return nil, fmt.Errorf("workspace: id %q not found", id)
}

// Current returns the active workspace, or nil if none is set.
func (m *Manager) Current() *Workspace {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, w := range m.items {
		if w.ID == m.currentID {
			out := w
			return &out
		}
	}
	return nil
}

package slack

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// SessionMap maps a Slack thread anchor (channel + thread_ts) to an overkill
// session id. Persisted as JSON so the bot can resume in-flight threads
// across restarts. New thread → new session; replies inside a thread reuse
// the same session.
type SessionMap struct {
	path string
	mu   sync.Mutex
	data map[string]sessionEntry // key = channel + ":" + threadTS
}

type sessionEntry struct {
	SessionID string    `json:"session_id"`
	Created   time.Time `json:"created"`
}

// NewSessionMap loads the persisted map from path. A missing file is not an
// error — we start with an empty map. An empty path disables persistence
// (used by tests).
func NewSessionMap(path string) (*SessionMap, error) {
	m := &SessionMap{path: path, data: map[string]sessionEntry{}}
	if path == "" {
		return m, nil
	}
	if data, err := os.ReadFile(path); err == nil {
		// Tolerate empty / partial files: log nothing, just start fresh.
		_ = json.Unmarshal(data, &m.data)
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("slack: load session map: %w", err)
	}
	return m, nil
}

// Get returns (sessionID, true) if a session exists for the thread.
func (m *SessionMap) Get(channel, threadTS string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.data[key(channel, threadTS)]
	return e.SessionID, ok
}

// GetOrCreate returns the existing session id for the thread, or registers
// `newID` and returns it. The created flag tells the caller whether they
// just minted a new session.
func (m *SessionMap) GetOrCreate(channel, threadTS, newID string) (id string, created bool, err error) {
	m.mu.Lock()
	k := key(channel, threadTS)
	if e, ok := m.data[k]; ok {
		m.mu.Unlock()
		return e.SessionID, false, nil
	}
	m.data[k] = sessionEntry{SessionID: newID, Created: time.Now().UTC()}
	snap := m.snapshotLocked()
	m.mu.Unlock()
	if err := m.persist(snap); err != nil {
		return newID, true, err
	}
	return newID, true, nil
}

func (m *SessionMap) snapshotLocked() map[string]sessionEntry {
	out := make(map[string]sessionEntry, len(m.data))
	for k, v := range m.data {
		out[k] = v
	}
	return out
}

func (m *SessionMap) persist(snap map[string]sessionEntry) error {
	if m.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(m.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, m.path)
}

func key(channel, threadTS string) string { return channel + ":" + threadTS }

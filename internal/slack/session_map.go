package slack

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/lib/pq"
)

// SessionMap maps a Slack thread anchor (channel + thread_ts) to an overkill
// session id. Persisted as JSON so the bot can resume in-flight threads
// across restarts. New thread → new session; replies inside a thread reuse
// the same session.
type SessionMap struct {
	path      string
	db        *sql.DB
	tableName string
	mu        sync.Mutex
	data      map[string]sessionEntry // key = channel + ":" + threadTS
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

// NewSessionMapDB creates a SessionMap backed by a Postgres table. The table
// is auto-created if it doesn't exist. All existing rows are loaded into
// memory on construction so lookups are fast; writes are persisted to both
// memory and Postgres on every GetOrCreate.
func NewSessionMapDB(db *sql.DB, tableName string) (*SessionMap, error) {
	ddl := fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s (
		channel    TEXT NOT NULL,
		thread_ts  TEXT NOT NULL,
		session_id TEXT NOT NULL,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		PRIMARY KEY (channel, thread_ts)
	)`, pq.QuoteIdentifier(tableName))
	if _, err := db.Exec(ddl); err != nil {
		return nil, fmt.Errorf("slack: create session_map table %s: %w", tableName, err)
	}

	rows, err := db.Query(fmt.Sprintf(`SELECT channel, thread_ts, session_id, created_at FROM %s`,
		pq.QuoteIdentifier(tableName)))
	if err != nil {
		return nil, fmt.Errorf("slack: load session_map rows: %w", err)
	}
	defer rows.Close()

	m := &SessionMap{
		db:        db,
		tableName: tableName,
		data:      make(map[string]sessionEntry),
	}
	for rows.Next() {
		var channel, threadTS, sessionID string
		var created time.Time
		if err := rows.Scan(&channel, &threadTS, &sessionID, &created); err != nil {
			return nil, fmt.Errorf("slack: scan session_map row: %w", err)
		}
		m.data[key(channel, threadTS)] = sessionEntry{SessionID: sessionID, Created: created}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("slack: iterate session_map rows: %w", err)
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
	defer m.mu.Unlock()
	k := key(channel, threadTS)
	if e, ok := m.data[k]; ok {
		return e.SessionID, false, nil
	}
	m.data[k] = sessionEntry{SessionID: newID, Created: time.Now().UTC()}
	snap := m.snapshotLocked()
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
	if m.db != nil {
		return m.persistDB(snap)
	}
	if m.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(m.path), 0o750); err != nil {
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

func (m *SessionMap) persistDB(snap map[string]sessionEntry) error {
	upsert := fmt.Sprintf(`INSERT INTO %s (channel, thread_ts, session_id, created_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (channel, thread_ts) DO UPDATE SET session_id = $3, created_at = $4`,
		pq.QuoteIdentifier(m.tableName))
	for k, v := range snap {
		channel, threadTS := splitKey(k)
		if _, err := m.db.Exec(upsert, channel, threadTS, v.SessionID, v.Created); err != nil {
			return fmt.Errorf("slack: upsert session_map: %w", err)
		}
	}
	return nil
}

func splitKey(k string) (channel, threadTS string) {
	if idx := strings.LastIndex(k, ":"); idx >= 0 {
		return k[:idx], k[idx+1:]
	}
	return k, ""
}

func key(channel, threadTS string) string { return channel + ":" + threadTS }

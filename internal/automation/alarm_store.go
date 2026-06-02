// Package automation — alarm persistence backed by PostgreSQL.
package automation

import (
	"database/sql"
	"fmt"
	"sync"
	"time"

	_ "github.com/lib/pq"
)

// AlarmStore is the persistence surface for alarms.
type AlarmStore interface {
	Save(a *Alarm) error
	Load() ([]*Alarm, error)
	Delete(id string) error
}

// MemoryAlarmStore is the test/no-persistence implementation.
type MemoryAlarmStore struct {
	mu     sync.RWMutex
	alarms map[string]*Alarm
}

func NewMemoryAlarmStore() *MemoryAlarmStore {
	return &MemoryAlarmStore{alarms: map[string]*Alarm{}}
}

func (s *MemoryAlarmStore) Save(a *Alarm) error {
	if a == nil {
		return fmt.Errorf("alarm store: nil alarm")
	}
	if a.ID == "" {
		return fmt.Errorf("alarm store: empty ID")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *a
	s.alarms[a.ID] = &cp
	return nil
}

func (s *MemoryAlarmStore) Load() ([]*Alarm, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Alarm, 0, len(s.alarms))
	for _, a := range s.alarms {
		cp := *a
		out = append(out, &cp)
	}
	sortAlarmsByFireAt(out)
	return out, nil
}

func (s *MemoryAlarmStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.alarms, id)
	return nil
}

// PostgresAlarmStore is the production implementation.
type PostgresAlarmStore struct {
	db *sql.DB
}

// NewPostgresAlarmStore wires a *sql.DB. The DB lifecycle is owned by the caller.
func NewPostgresAlarmStore(db *sql.DB) (*PostgresAlarmStore, error) {
	s := &PostgresAlarmStore{db: db}
	if err := s.migrate(); err != nil {
		return nil, fmt.Errorf("alarm store: migrate: %w", err)
	}
	return s, nil
}

func (s *PostgresAlarmStore) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS automation_alarms (
			id          TEXT PRIMARY KEY,
			name        TEXT NOT NULL DEFAULT '',
			fire_at     TIMESTAMPTZ,
			action      TEXT NOT NULL DEFAULT '',
			prompt      TEXT NOT NULL DEFAULT '',
			session_id  TEXT NOT NULL DEFAULT '',
			fired       BOOLEAN NOT NULL DEFAULT false,
			cancelled   BOOLEAN NOT NULL DEFAULT false,
			fired_at    TIMESTAMPTZ,
			result      TEXT NOT NULL DEFAULT '',
			attempts    INTEGER NOT NULL DEFAULT 0,
			created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_alarms_fire_at ON automation_alarms (fire_at)`)
	return err
}

func (s *PostgresAlarmStore) Save(a *Alarm) error {
	if a == nil {
		return fmt.Errorf("alarm store: nil alarm")
	}
	if a.ID == "" {
		return fmt.Errorf("alarm store: empty ID")
	}
	_, err := s.db.Exec(`
		INSERT INTO automation_alarms (id, name, fire_at, action, prompt, session_id, fired, cancelled, fired_at, result, attempts, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, NOW())
		ON CONFLICT (id) DO UPDATE SET
			name       = EXCLUDED.name,
			fire_at    = EXCLUDED.fire_at,
			action     = EXCLUDED.action,
			prompt     = EXCLUDED.prompt,
			session_id = EXCLUDED.session_id,
			fired      = EXCLUDED.fired,
			cancelled  = EXCLUDED.cancelled,
			fired_at   = EXCLUDED.fired_at,
			result     = EXCLUDED.result,
			attempts   = EXCLUDED.attempts
	`, a.ID, a.Name, nullableTime(a.FireAt), a.Action, a.Prompt, a.SessionID,
		a.Fired, a.Cancelled, nullableTime(a.FiredAt), a.Result, a.Attempts)
	if err != nil {
		return fmt.Errorf("alarm store: save %s: %w", a.ID, err)
	}
	return nil
}

func (s *PostgresAlarmStore) Load() ([]*Alarm, error) {
	rows, err := s.db.Query(`
		SELECT id, name, fire_at, action, prompt, session_id, fired, cancelled, fired_at, result, attempts
		FROM automation_alarms ORDER BY fire_at
	`)
	if err != nil {
		return nil, fmt.Errorf("alarm store: load: %w", err)
	}
	defer rows.Close()

	var out []*Alarm
	for rows.Next() {
		var a Alarm
		var fireAt, firedAt sql.NullTime
		var attempts int
		if err := rows.Scan(&a.ID, &a.Name, &fireAt, &a.Action, &a.Prompt, &a.SessionID,
			&a.Fired, &a.Cancelled, &firedAt, &a.Result, &attempts); err != nil {
			// Skip corrupt rows
			continue
		}
		if fireAt.Valid {
			a.FireAt = fireAt.Time
		}
		if firedAt.Valid {
			a.FiredAt = firedAt.Time
		}
		a.Attempts = attempts
		out = append(out, &a)
	}
	sortAlarmsByFireAt(out)
	return out, rows.Err()
}

func (s *PostgresAlarmStore) Delete(id string) error {
	_, err := s.db.Exec(`DELETE FROM automation_alarms WHERE id = $1`, id)
	return err
}

// sortAlarmsByFireAt sorts in place by FireAt ascending.
func sortAlarmsByFireAt(alarms []*Alarm) {
	for i := 1; i < len(alarms); i++ {
		for j := i; j > 0 && alarms[j].FireAt.Before(alarms[j-1].FireAt); j-- {
			alarms[j], alarms[j-1] = alarms[j-1], alarms[j]
		}
	}
}

func nullableTime(t time.Time) interface{} {
	if t.IsZero() {
		return nil
	}
	return t
}

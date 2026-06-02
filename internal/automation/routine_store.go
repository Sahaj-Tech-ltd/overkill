// Package automation — routine persistence (§7.1 Layer 4).
//
// Mirrors the AlarmStore shape: a small Save/Load/Delete interface
// with a MemoryRoutineStore for tests and a PostgresRoutineStore for
// the daemon. Same *sql.DB as alarms + SOPs, different table.
package automation

import (
	"database/sql"
	"fmt"
	"sort"
	"sync"
	"time"

	_ "github.com/lib/pq"
)

// RoutineStore is the minimal surface the engine uses.
type RoutineStore interface {
	Save(r *Routine) error
	Load() ([]*Routine, error)
	Delete(id string) error
}

// MemoryRoutineStore is the test/no-persistence impl.
type MemoryRoutineStore struct {
	mu       sync.RWMutex
	routines map[string]*Routine
}

func NewMemoryRoutineStore() *MemoryRoutineStore {
	return &MemoryRoutineStore{routines: map[string]*Routine{}}
}

func (s *MemoryRoutineStore) Save(r *Routine) error {
	if r == nil {
		return fmt.Errorf("routine store: nil routine")
	}
	if r.ID == "" {
		return fmt.Errorf("routine store: empty ID")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *r
	s.routines[r.ID] = &cp
	return nil
}

func (s *MemoryRoutineStore) Load() ([]*Routine, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Routine, 0, len(s.routines))
	for _, r := range s.routines {
		cp := *r
		out = append(out, &cp)
	}
	sortRoutinesByID(out)
	return out, nil
}

func (s *MemoryRoutineStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.routines, id)
	return nil
}

// PostgresRoutineStore persists routines in PostgreSQL.
type PostgresRoutineStore struct {
	db *sql.DB
}

func NewPostgresRoutineStore(db *sql.DB) (*PostgresRoutineStore, error) {
	s := &PostgresRoutineStore{db: db}
	if err := s.migrate(); err != nil {
		return nil, fmt.Errorf("routine store: migrate: %w", err)
	}
	return s, nil
}

func (s *PostgresRoutineStore) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS automation_routines (
			id         TEXT PRIMARY KEY,
			name       TEXT NOT NULL DEFAULT '',
			trigger    TEXT NOT NULL DEFAULT '',
			action     TEXT NOT NULL DEFAULT '',
			cooldown_ns BIGINT NOT NULL DEFAULT 0,
			enabled    BOOLEAN NOT NULL DEFAULT true,
			last_fired TIMESTAMPTZ,
			fire_count INTEGER NOT NULL DEFAULT 0,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`)
	return err
}

func (s *PostgresRoutineStore) Save(r *Routine) error {
	if r == nil {
		return fmt.Errorf("routine store: nil routine")
	}
	if r.ID == "" {
		return fmt.Errorf("routine store: empty ID")
	}
	_, err := s.db.Exec(`
		INSERT INTO automation_routines (id, name, trigger, action, cooldown_ns, enabled, last_fired, fire_count, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, NOW())
		ON CONFLICT (id) DO UPDATE SET
			name        = EXCLUDED.name,
			trigger     = EXCLUDED.trigger,
			action      = EXCLUDED.action,
			cooldown_ns = EXCLUDED.cooldown_ns,
			enabled     = EXCLUDED.enabled,
			last_fired  = EXCLUDED.last_fired,
			fire_count  = EXCLUDED.fire_count
	`, r.ID, r.Name, r.Trigger, r.Action, int64(r.Cooldown), r.Enabled,
		nullableTime(r.LastFired), r.FireCount)
	if err != nil {
		return fmt.Errorf("routine store: save %s: %w", r.ID, err)
	}
	return nil
}

func (s *PostgresRoutineStore) Load() ([]*Routine, error) {
	rows, err := s.db.Query(`
		SELECT id, name, trigger, action, cooldown_ns, enabled, last_fired, fire_count
		FROM automation_routines ORDER BY id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Routine
	for rows.Next() {
		var r Routine
		var cooldownNs int64
		var lastFired sql.NullTime
		if err := rows.Scan(&r.ID, &r.Name, &r.Trigger, &r.Action, &cooldownNs,
			&r.Enabled, &lastFired, &r.FireCount); err != nil {
			return nil, fmt.Errorf("routine store: parse: %w", err)
		}
		r.Cooldown = time.Duration(cooldownNs)
		if lastFired.Valid {
			r.LastFired = lastFired.Time
		}
		out = append(out, &r)
	}
	sortRoutinesByID(out)
	return out, rows.Err()
}

func (s *PostgresRoutineStore) Delete(id string) error {
	_, err := s.db.Exec(`DELETE FROM automation_routines WHERE id = $1`, id)
	return err
}

// sortRoutinesByID is a deterministic ordering.
func sortRoutinesByID(rs []*Routine) {
	sort.Slice(rs, func(i, j int) bool { return rs[i].ID < rs[j].ID })
}

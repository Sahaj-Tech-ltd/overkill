// Package agent — PostgreSQL-backed FlowStore for the daemon.
package agent

import (
	"database/sql"
	"encoding/json"
	"fmt"

	_ "github.com/lib/pq"
)

// PostgresFlowStore persists FlowState to PostgreSQL under the table
// "flow_states". DB lifecycle is owned by the caller (the daemon).
type PostgresFlowStore struct {
	db *sql.DB
}

// NewPostgresFlowStore wires a store to an open *sql.DB.
func NewPostgresFlowStore(db *sql.DB) (*PostgresFlowStore, error) {
	s := &PostgresFlowStore{db: db}
	if err := s.migrate(); err != nil {
		return nil, fmt.Errorf("flow store: migrate: %w", err)
	}
	return s, nil
}

func (s *PostgresFlowStore) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS flow_states (
			id          TEXT PRIMARY KEY,
			session_id  TEXT NOT NULL DEFAULT '',
			state       JSONB NOT NULL DEFAULT '{}',
			updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_flow_states_session ON flow_states (session_id)`)
	return err
}

// Save serializes state and writes it. Overwrites existing rows.
func (s *PostgresFlowStore) Save(state *FlowState) error {
	if state == nil {
		return fmt.Errorf("flow store: nil state")
	}
	if state.ID == "" {
		return fmt.Errorf("flow store: empty ID")
	}
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("flow store: marshal: %w", err)
	}
	_, err = s.db.Exec(`
		INSERT INTO flow_states (id, session_id, state, updated_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (id) DO UPDATE SET
			session_id = EXCLUDED.session_id,
			state = EXCLUDED.state,
			updated_at = EXCLUDED.updated_at
	`, state.ID, state.SessionID, data)
	if err != nil {
		return fmt.Errorf("flow store: save: %w", err)
	}
	return nil
}

// Load returns the state or (nil, nil) when the ID is missing.
func (s *PostgresFlowStore) Load(id string) (*FlowState, error) {
	var raw []byte
	err := s.db.QueryRow(`SELECT state FROM flow_states WHERE id = $1`, id).Scan(&raw)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("flow store: load: %w", err)
	}
	var state FlowState
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil, ErrFlowCorrupt
	}
	return &state, nil
}

// Delete removes a flow record. Missing keys are no-ops.
func (s *PostgresFlowStore) Delete(id string) error {
	_, err := s.db.Exec(`DELETE FROM flow_states WHERE id = $1`, id)
	return err
}

// List returns every parseable flow.
func (s *PostgresFlowStore) List() ([]*FlowState, error) {
	rows, err := s.db.Query(`SELECT state FROM flow_states ORDER BY updated_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("flow store: list: %w", err)
	}
	defer rows.Close()

	var out []*FlowState
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			continue
		}
		var state FlowState
		if err := json.Unmarshal(raw, &state); err != nil {
			continue // skip corrupt rows
		}
		out = append(out, &state)
	}
	return out, rows.Err()
}

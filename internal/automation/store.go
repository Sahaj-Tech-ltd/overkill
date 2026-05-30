package automation

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "github.com/lib/pq"
)

// PostgresSOPStore persists SOPs with PostgreSQL.
type PostgresSOPStore struct {
	db *sql.DB
}

// NewPostgresSOPStore wires a store to an open *sql.DB.
func NewPostgresSOPStore(db *sql.DB) (*PostgresSOPStore, error) {
	s := &PostgresSOPStore{db: db}
	if err := s.migrate(); err != nil {
		return nil, fmt.Errorf("automation: sop migrate: %w", err)
	}
	return s, nil
}

func (s *PostgresSOPStore) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS automation_sops (
			id            TEXT PRIMARY KEY,
			name          TEXT NOT NULL DEFAULT '',
			description   TEXT NOT NULL DEFAULT '',
			mode          INTEGER NOT NULL DEFAULT 0,
			steps         JSONB NOT NULL DEFAULT '[]',
			status        TEXT NOT NULL DEFAULT 'draft',
			current_step  INTEGER NOT NULL DEFAULT 0,
			metadata      JSONB NOT NULL DEFAULT '{}',
			created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`)
	return err
}

func (s *PostgresSOPStore) SaveSOP(sop *SOP) error {
	stepsJSON, err := json.Marshal(sop.Steps)
	if err != nil {
		return fmt.Errorf("automation: marshal steps: %w", err)
	}
	metaJSON, err := json.Marshal(sop.Metadata)
	if err != nil {
		return fmt.Errorf("automation: marshal metadata: %w", err)
	}

	now := time.Now()
	if sop.CreatedAt.IsZero() {
		sop.CreatedAt = now
	}
	sop.UpdatedAt = now

	_, err = s.db.Exec(`
		INSERT INTO automation_sops (id, name, description, mode, steps, status, current_step, metadata, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (id) DO UPDATE SET
			name         = EXCLUDED.name,
			description  = EXCLUDED.description,
			mode         = EXCLUDED.mode,
			steps        = EXCLUDED.steps,
			status       = EXCLUDED.status,
			current_step = EXCLUDED.current_step,
			metadata     = EXCLUDED.metadata,
			updated_at   = EXCLUDED.updated_at
	`, sop.ID, sop.Name, sop.Description, int(sop.Mode), stepsJSON,
		string(sop.Status), sop.CurrentStep, metaJSON, sop.CreatedAt, sop.UpdatedAt)
	if err != nil {
		return fmt.Errorf("automation: save SOP %s: %w", sop.ID, err)
	}
	return nil
}

func (s *PostgresSOPStore) LoadSOPs() ([]SOP, error) {
	rows, err := s.db.Query(`
		SELECT id, name, description, mode, steps, status, current_step, metadata, created_at, updated_at
		FROM automation_sops ORDER BY created_at
	`)
	if err != nil {
		return nil, fmt.Errorf("automation: load SOPs: %w", err)
	}
	defer rows.Close()

	var sops []SOP
	for rows.Next() {
		var sop SOP
		var modeInt int
		var statusStr string
		var stepsJSON, metaJSON []byte
		if err := rows.Scan(&sop.ID, &sop.Name, &sop.Description, &modeInt, &stepsJSON,
			&statusStr, &sop.CurrentStep, &metaJSON, &sop.CreatedAt, &sop.UpdatedAt); err != nil {
			return nil, fmt.Errorf("automation: scan SOP: %w", err)
		}
		sop.Mode = SOPMode(modeInt)
		sop.Status = SOPStatus(statusStr)
		_ = json.Unmarshal(stepsJSON, &sop.Steps)
		_ = json.Unmarshal(metaJSON, &sop.Metadata)
		if sop.Metadata == nil {
			sop.Metadata = make(map[string]string)
		}
		sops = append(sops, sop)
	}
	return sops, rows.Err()
}

func (s *PostgresSOPStore) DeleteSOP(id string) error {
	result, err := s.db.Exec(`DELETE FROM automation_sops WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("automation: delete SOP %s: %w", id, err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

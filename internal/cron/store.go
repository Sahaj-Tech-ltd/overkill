package cron

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "github.com/lib/pq"
)

// PostgresJobStore persists cron jobs in PostgreSQL.
type PostgresJobStore struct {
	db *sql.DB
}

func NewPostgresJobStore(db *sql.DB) (*PostgresJobStore, error) {
	s := &PostgresJobStore{db: db}
	if err := s.migrate(); err != nil {
		return nil, fmt.Errorf("cron: migrate: %w", err)
	}
	return s, nil
}

func (s *PostgresJobStore) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS cron_jobs (
			id              TEXT PRIMARY KEY,
			name            TEXT NOT NULL DEFAULT '',
			schedule        TEXT NOT NULL DEFAULT '',
			timezone        TEXT NOT NULL DEFAULT 'UTC',
			command         TEXT NOT NULL DEFAULT '',
			execution_style TEXT NOT NULL DEFAULT 'main',
			session_id      TEXT NOT NULL DEFAULT '',
			status          TEXT NOT NULL DEFAULT 'active',
			enabled         BOOLEAN NOT NULL DEFAULT true,
			last_run        TIMESTAMPTZ,
			next_run        TIMESTAMPTZ,
			run_count       INTEGER NOT NULL DEFAULT 0,
			failure_count   INTEGER NOT NULL DEFAULT 0,
			max_retries     INTEGER NOT NULL DEFAULT 3,
			metadata        JSONB NOT NULL DEFAULT '{}',
			created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`)
	return err
}

func (s *PostgresJobStore) SaveJob(job *Job) error {
	metaBytes, err := json.Marshal(job.Metadata)
	if err != nil {
		return fmt.Errorf("cron: marshaling metadata: %w", err)
	}
	if metaBytes == nil {
		metaBytes = []byte("{}")
	}

	_, err = s.db.Exec(`
		INSERT INTO cron_jobs (id, name, schedule, timezone, command, execution_style, session_id, status, enabled, last_run, next_run, run_count, failure_count, max_retries, metadata, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
		ON CONFLICT (id) DO UPDATE SET
			name            = EXCLUDED.name,
			schedule        = EXCLUDED.schedule,
			timezone        = EXCLUDED.timezone,
			command         = EXCLUDED.command,
			execution_style = EXCLUDED.execution_style,
			session_id      = EXCLUDED.session_id,
			status          = EXCLUDED.status,
			enabled         = EXCLUDED.enabled,
			last_run        = EXCLUDED.last_run,
			next_run        = EXCLUDED.next_run,
			run_count       = EXCLUDED.run_count,
			failure_count   = EXCLUDED.failure_count,
			max_retries     = EXCLUDED.max_retries,
			metadata        = EXCLUDED.metadata
	`,
		job.ID, job.Name, job.Schedule, job.Timezone, job.Command,
		string(job.ExecutionStyle), job.SessionID, string(job.Status), true,
		nullableTime(job.LastRun), nullableTime(job.NextRun),
		job.RunCount, job.FailureCount, job.MaxRetries, metaBytes, job.CreatedAt)
	if err != nil {
		return fmt.Errorf("cron: saving job: %w", err)
	}
	return nil
}

func (s *PostgresJobStore) LoadJobs() ([]Job, error) {
	rows, err := s.db.Query(`
		SELECT id, name, schedule, timezone, command, execution_style, session_id, status, last_run, next_run, run_count, failure_count, max_retries, metadata, created_at
		FROM cron_jobs ORDER BY created_at
	`)
	if err != nil {
		return nil, fmt.Errorf("cron: loading jobs: %w", err)
	}
	defer rows.Close()

	var jobs []Job
	for rows.Next() {
		var j Job
		var style string
		var lastRun, nextRun sql.NullTime
		var metaBytes []byte
		if err := rows.Scan(&j.ID, &j.Name, &j.Schedule, &j.Timezone, &j.Command,
			&style, &j.SessionID, &j.Status, &lastRun, &nextRun,
			&j.RunCount, &j.FailureCount, &j.MaxRetries, &metaBytes, &j.CreatedAt); err != nil {
			continue
		}
		j.ExecutionStyle = ExecutionStyle(style)
		if lastRun.Valid {
			j.LastRun = lastRun.Time
		}
		if nextRun.Valid {
			j.NextRun = nextRun.Time
		}
		j.Metadata = make(map[string]string)
		if len(metaBytes) > 0 {
			_ = json.Unmarshal(metaBytes, &j.Metadata)
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

func (s *PostgresJobStore) DeleteJob(id string) error {
	result, err := s.db.Exec(`DELETE FROM cron_jobs WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("cron: deleting job: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return ErrJobNotFound
	}
	return nil
}

func nullableTime(t time.Time) interface{} {
	if t.IsZero() {
		return nil
	}
	return t
}

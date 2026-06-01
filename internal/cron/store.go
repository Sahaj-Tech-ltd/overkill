package cron

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
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
	ctx := context.Background()
	queries := []string{
		`CREATE TABLE IF NOT EXISTS cron_jobs (
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
		)`,
		`CREATE TABLE IF NOT EXISTS cron_run_logs (
			id          BIGSERIAL PRIMARY KEY,
			job_id      TEXT NOT NULL REFERENCES cron_jobs(id) ON DELETE CASCADE,
			output      TEXT NOT NULL DEFAULT '',
			error       TEXT NOT NULL DEFAULT '',
			success     BOOLEAN NOT NULL DEFAULT false,
			run_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_cron_jobs_session_id ON cron_jobs (session_id)`,
		`CREATE INDEX IF NOT EXISTS idx_cron_jobs_status ON cron_jobs (status)`,
		`CREATE INDEX IF NOT EXISTS idx_cron_run_logs_job_id ON cron_run_logs (job_id)`,
		`CREATE INDEX IF NOT EXISTS idx_cron_run_logs_run_at ON cron_run_logs (run_at)`,
	}
	for _, q := range queries {
		if _, err := s.db.ExecContext(ctx, q); err != nil {
			return err
		}
	}
	return nil
}

func (s *PostgresJobStore) SaveJob(ctx context.Context, job *Job) error {
	metaBytes, err := json.Marshal(job.Metadata)
	if err != nil {
		return fmt.Errorf("cron: marshaling metadata: %w", err)
	}

	_, err = s.db.ExecContext(ctx, `
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

func (s *PostgresJobStore) LoadJobs(ctx context.Context) ([]Job, error) {
	rows, err := s.db.QueryContext(ctx, `
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
			log.Printf("cron: scan error: %v", err)
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
			if err := json.Unmarshal(metaBytes, &j.Metadata); err != nil {
				log.Printf("cron: unmarshal metadata: %v", err)
			}
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

func (s *PostgresJobStore) DeleteJob(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM cron_jobs WHERE id = $1`, id)
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

// RunLog represents a single cron job execution record.
type RunLog struct {
	ID      int64     `json:"id"`
	JobID   string    `json:"job_id"`
	Output  string    `json:"output"`
	Error   string    `json:"error"`
	Success bool      `json:"success"`
	RunAt   time.Time `json:"run_at"`
}

// LogRun persists a cron job execution record. output may be empty when
// the onFire callback doesn't produce captured output text (gateway delivery
// is separate); the error field and success flag tell the operational story.
func (s *PostgresJobStore) LogRun(ctx context.Context, jobID, output, errMsg string, success bool) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO cron_run_logs (job_id, output, error, success) VALUES ($1, $2, $3, $4)`,
		jobID, output, errMsg, success,
	)
	if err != nil {
		return fmt.Errorf("cron: log run: %w", err)
	}
	return nil
}

// GetRunLogs returns the most recent run log entries for a job, newest first.
func (s *PostgresJobStore) GetRunLogs(ctx context.Context, jobID string, limit int) ([]RunLog, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, job_id, output, error, success, run_at
		 FROM cron_run_logs WHERE job_id = $1
		 ORDER BY run_at DESC LIMIT $2`, jobID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("cron: get run logs: %w", err)
	}
	defer rows.Close()
	var logs []RunLog
	for rows.Next() {
		var l RunLog
		if err := rows.Scan(&l.ID, &l.JobID, &l.Output, &l.Error, &l.Success, &l.RunAt); err != nil {
			return nil, fmt.Errorf("cron: scan run log: %w", err)
		}
		logs = append(logs, l)
	}
	return logs, rows.Err()
}

// Package db provides a shared PostgreSQL connection and table migration
// for all Overkill persistent stores. Call Open once at boot, pass the
// *sql.DB to every store constructor.
//
// Each store creates its own table with CREATE TABLE IF NOT EXISTS on
// construction — no separate migration tool needed.
package db

import (
	"database/sql"
	"fmt"
	"time"

	_ "github.com/lib/pq"
)

// Open connects to PostgreSQL and runs all table migrations.
// connStr should be a PostgreSQL connection string, e.g.
// "postgres://user:***@localhost:5432/overkill?sslmode=disable".
func Open(connStr string) (*sql.DB, error) {
	database, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("db: opening postgres: %w", err)
	}
	if err := database.Ping(); err != nil {
		database.Close()
		return nil, fmt.Errorf("db: pinging postgres: %w", err)
	}
	database.SetMaxOpenConns(25)
	database.SetMaxIdleConns(5)
	database.SetConnMaxLifetime(30 * time.Minute)
	database.SetConnMaxIdleTime(5 * time.Minute)
	return database, nil
}

// Migrate creates all Overkill tables. Idempotent — safe to call on
// every boot. Individual stores may also call their own CREATE TABLE
// IF NOT EXISTS, but centralizing here keeps the schema visible.
func Migrate(db *sql.DB) error {
	tables := []string{
		// Sessions — migrated by session.NewPostgresStore (unified schema).
		// DO NOT duplicate the sessions CREATE here — the canonical schema is
		// in internal/session/postgres.go.

		// Session router bindings — owned by internal/session/postgres.go.
		// DO NOT duplicate the session_router CREATE here.

		// Cost tracking — migrated by cost.NewPostgresTracker (cost_records table).
		// DO NOT duplicate cost table creation here.

		// Cron jobs — owned by internal/cron/store.go.
		// DO NOT duplicate the cron_jobs CREATE here.

		// Daemon jobs — owned by internal/daemon/jobs.go.
		// DO NOT duplicate the daemon_jobs CREATE here.

		// Agent flow state
		`CREATE TABLE IF NOT EXISTS agent_flows (
			id          TEXT PRIMARY KEY,
			session_id  TEXT NOT NULL,
			state       JSONB NOT NULL DEFAULT '{}',
			step        INTEGER NOT NULL DEFAULT 0,
			updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_flows_session ON agent_flows (session_id)`,

		// Goals
		`CREATE TABLE IF NOT EXISTS goals (
			session_id  TEXT PRIMARY KEY,
			text        TEXT NOT NULL DEFAULT '',
			active      BOOLEAN NOT NULL DEFAULT true,
			created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,

		// Input history
		`CREATE TABLE IF NOT EXISTS input_history (
			chat_key    TEXT PRIMARY KEY,
			messages    JSONB NOT NULL DEFAULT '[]',
			updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,

		// Agent suspensions
		`CREATE TABLE IF NOT EXISTS suspensions (
			id           TEXT PRIMARY KEY,
			session_id   TEXT NOT NULL,
			state        JSONB NOT NULL DEFAULT '{}',
			suspended_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_suspensions_session ON suspensions (session_id)`,

		// Memory entries (cross-session knowledge)
		`CREATE TABLE IF NOT EXISTS memory_entries (
			id         SERIAL PRIMARY KEY,
			key        TEXT NOT NULL UNIQUE,
			value      JSONB NOT NULL DEFAULT '{}',
			category   TEXT NOT NULL DEFAULT 'general',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,

		// Automation routines
		`CREATE TABLE IF NOT EXISTS automation_routines (
			id         TEXT PRIMARY KEY,
			name       TEXT NOT NULL DEFAULT '',
			steps      JSONB NOT NULL DEFAULT '[]',
			enabled    BOOLEAN NOT NULL DEFAULT true,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,

		// Automation alarms
		`CREATE TABLE IF NOT EXISTS automation_alarms (
			id         TEXT PRIMARY KEY,
			name       TEXT NOT NULL DEFAULT '',
			schedule   TEXT NOT NULL,
			action     TEXT NOT NULL DEFAULT '',
			enabled    BOOLEAN NOT NULL DEFAULT true,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,

		// Bookmarks (session bookmarks for gateway /bm)
		`CREATE TABLE IF NOT EXISTS bookmarks (
			id          SERIAL PRIMARY KEY,
			session_id  TEXT NOT NULL,
			label       TEXT NOT NULL,
			created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_bookmarks_session ON bookmarks (session_id)`,
		`CREATE INDEX IF NOT EXISTS idx_bookmarks_label   ON bookmarks (label)`,

		// Learning corrections (mirrors what learning/store.go already creates,
		// but centralizing for visibility)
		// Already handled by learning.NewStore — no duplicate CREATE needed.
	}

	for _, ddl := range tables {
		if _, err := db.Exec(ddl); err != nil {
			return fmt.Errorf("db: migration: %w\nsql: %s", err, ddl)
		}
	}
	return nil
}

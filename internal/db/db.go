// Package db provides shared PostgreSQL connections and central bootstrap
// migrations. Call Open once at boot, pass the
// *sql.DB to every store constructor.
//
// Independent persistent stores retain their own constructor migrations.
package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/dbschema"
	_ "github.com/lib/pq"
)

// Open connects to PostgreSQL and verifies connectivity.
// connStr should be a PostgreSQL connection string, e.g.
// "postgres://user:***@localhost:5432/overkill?sslmode=disable".
func Open(connStr string) (*sql.DB, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return OpenContext(ctx, connStr)
}

// OpenContext opens a shared pool and verifies connectivity within the caller's
// deadline. The caller owns the returned pool.
func OpenContext(ctx context.Context, connStr string) (*sql.DB, error) {
	database, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("db: opening postgres: %w", err)
	}
	if err := database.PingContext(ctx); err != nil {
		database.Close()
		return nil, fmt.Errorf("db: pinging postgres: %w", err)
	}
	database.SetMaxOpenConns(25)
	database.SetMaxIdleConns(5)
	database.SetConnMaxLifetime(30 * time.Minute)
	database.SetConnMaxIdleTime(5 * time.Minute)
	return database, nil
}

// Migrate atomically creates bootstrap-owned tables and upgrades shared
// suspension/routine, goal and gateway definitions. Other stores migrate separately.
// Concurrent updated bootstraps coordinate with shared-store owner locks.
func Migrate(db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("db: database is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
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

		// Goals are owned by dbschema.Goals.

		// Input history is owned by dbschema.GatewayInputHistory.

		// Suspensions are owned by dbschema.Suspensions.

		// Memory entries (cross-session knowledge)
		`CREATE TABLE IF NOT EXISTS memory_entries (
			id         SERIAL PRIMARY KEY,
			key        TEXT NOT NULL UNIQUE,
			value      JSONB NOT NULL DEFAULT '{}',
			category   TEXT NOT NULL DEFAULT 'general',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,

		// Routines are owned by dbschema.Routines.

		// Automation alarms
		`CREATE TABLE IF NOT EXISTS automation_alarms (
			id         TEXT PRIMARY KEY,
			name       TEXT NOT NULL DEFAULT '',
			schedule   TEXT NOT NULL,
			action     TEXT NOT NULL DEFAULT '',
			enabled    BOOLEAN NOT NULL DEFAULT true,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,

		// Bookmarks are owned by dbschema.GatewayBookmarks.

		// Learning corrections (mirrors what learning/store.go already creates,
		// but centralizing for visibility)
		// Already handled by learning.NewStore — no duplicate CREATE needed.
	}

	if err := dbschema.Bootstrap(ctx, db, tables); err != nil {
		return fmt.Errorf("db: migration: %w", err)
	}
	return nil
}

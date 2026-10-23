// Package dbschema owns SQL shared by application bootstrap and store startup.
// Keeping each definition here prevents CREATE IF NOT EXISTS from hiding
// incompatible definitions in independent entry points.
package dbschema

import (
	"context"
	"database/sql"
	"fmt"
)

type migrationGroup struct {
	name       string
	statements []string
}

func apply(ctx context.Context, db *sql.DB, groups ...migrationGroup) error {
	if db == nil {
		return fmt.Errorf("schema: database is required")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("schema: begin migration: %w", err)
	}
	defer tx.Rollback()
	// Lock all participating schema owners before any DDL. Bootstrap uses the
	// same owner keys as standalone store constructors, in this fixed order.
	for _, group := range groups {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || $1, 0))`, ":overkill:"+group.name); err != nil {
			return fmt.Errorf("schema %s: lock migration: %w", group.name, err)
		}
	}
	for _, group := range groups {
		for i, statement := range group.statements {
			if _, err := tx.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("schema %s: statement %d: %w", group.name, i+1, err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("schema: commit migration: %w", err)
	}
	return nil
}

// Bootstrap applies shared-store upgrades (including goals and gateway state)
// and central tables atomically.
// Independent application stores retain their own separate migrations.
func Bootstrap(ctx context.Context, db *sql.DB, tables []string) error {
	return apply(ctx, db, migrationGroup{"suspensions", suspensionStatements},
		migrationGroup{"automation_routines", routineStatements},
		migrationGroup{"goals", goalStatements},
		migrationGroup{"gateway_bookmarks", bookmarkStatements},
		migrationGroup{"gateway_input_history", inputHistoryStatements},
		migrationGroup{"bootstrap", tables})
}

func Suspensions(ctx context.Context, db *sql.DB) error {
	return apply(ctx, db, migrationGroup{"suspensions", suspensionStatements})
}

func Routines(ctx context.Context, db *sql.DB) error {
	return apply(ctx, db, migrationGroup{"automation_routines", routineStatements})
}

var suspensionStatements = []string{
	`CREATE TABLE IF NOT EXISTS suspensions (
			id TEXT PRIMARY KEY, session_id TEXT NOT NULL,
			tool_name TEXT NOT NULL DEFAULT '', args TEXT NOT NULL DEFAULT '',
			risk TEXT NOT NULL DEFAULT '', call_data JSONB NOT NULL DEFAULT '{}',
			suspended_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`,
	// Previous central bootstrap created only id/session_id/state/time.
	// Preserve its state column and rows while adding the fields used by the store.
	`ALTER TABLE suspensions ADD COLUMN IF NOT EXISTS tool_name TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE suspensions ADD COLUMN IF NOT EXISTS args TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE suspensions ADD COLUMN IF NOT EXISTS risk TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE suspensions ADD COLUMN IF NOT EXISTS call_data JSONB NOT NULL DEFAULT '{}'`,
	`CREATE INDEX IF NOT EXISTS idx_suspensions_session ON suspensions (session_id)`,
}

var routineStatements = []string{
	`CREATE TABLE IF NOT EXISTS automation_routines (
			id TEXT PRIMARY KEY, name TEXT NOT NULL DEFAULT '',
			trigger TEXT NOT NULL DEFAULT '', action TEXT NOT NULL DEFAULT '',
			cooldown_ns BIGINT NOT NULL DEFAULT 0, enabled BOOLEAN NOT NULL DEFAULT true,
			last_fired TIMESTAMPTZ, fire_count INTEGER NOT NULL DEFAULT 0,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`,
	// Preserve legacy steps data; it cannot safely be inferred as a trigger/action.
	`ALTER TABLE automation_routines ADD COLUMN IF NOT EXISTS trigger TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE automation_routines ADD COLUMN IF NOT EXISTS action TEXT NOT NULL DEFAULT ''`,
	`ALTER TABLE automation_routines ADD COLUMN IF NOT EXISTS cooldown_ns BIGINT NOT NULL DEFAULT 0`,
	`ALTER TABLE automation_routines ADD COLUMN IF NOT EXISTS last_fired TIMESTAMPTZ`,
	`ALTER TABLE automation_routines ADD COLUMN IF NOT EXISTS fire_count INTEGER NOT NULL DEFAULT 0`,
}

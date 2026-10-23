package dbschema

import (
	"context"
	"database/sql"
)

// Goals shares atomic upgrades, accounting backfill and ownership with bootstrap.
func Goals(ctx context.Context, db *sql.DB) error {
	return apply(ctx, db, migrationGroup{"goals", goalStatements})
}

var goalStatements = []string{
	`
		CREATE TABLE IF NOT EXISTS goals (
			session_id  TEXT PRIMARY KEY,
			text        TEXT NOT NULL DEFAULT '',
			active      BOOLEAN NOT NULL DEFAULT true,
			created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			token_budget       INTEGER NOT NULL DEFAULT 0,
			tokens_used        INTEGER NOT NULL DEFAULT 0,
			time_budget_seconds INTEGER NOT NULL DEFAULT 0,
			time_used_seconds  INTEGER NOT NULL DEFAULT 0,
			status       TEXT NOT NULL DEFAULT ''
		)
	`,
	`ALTER TABLE goals ADD COLUMN IF NOT EXISTS goal_id TEXT NOT NULL DEFAULT md5(random()::text || clock_timestamp()::text)`,
	`ALTER TABLE goals ADD COLUMN IF NOT EXISTS time_used_nanoseconds BIGINT NOT NULL DEFAULT 0`,
	`ALTER TABLE goals ADD COLUMN IF NOT EXISTS token_budget INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE goals ADD COLUMN IF NOT EXISTS tokens_used INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE goals ADD COLUMN IF NOT EXISTS time_budget_seconds INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE goals ADD COLUMN IF NOT EXISTS time_used_seconds INTEGER NOT NULL DEFAULT 0`,
	`ALTER TABLE goals ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT ''`,
	`UPDATE goals SET time_used_nanoseconds = time_used_seconds::bigint * 1000000000 WHERE time_used_nanoseconds = 0 AND time_used_seconds > 0`,
	`CREATE TABLE IF NOT EXISTS goal_turn_usage (
        turn_id TEXT PRIMARY KEY, goal_id TEXT NOT NULL, session_id TEXT NOT NULL,
        tokens_used BIGINT NOT NULL DEFAULT 0, time_used_nanoseconds BIGINT NOT NULL DEFAULT 0,
        started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), ended_at TIMESTAMPTZ
    )`,
}

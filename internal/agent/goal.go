package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "github.com/lib/pq"
)

// Goal is a standing objective that is injected into the system prompt
// every turn until paused or cleared.
type Goal struct {
	Text             string    `json:"text"`
	Active           bool      `json:"active"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
	TokenBudget      int       `json:"token_budget"`
	TokensUsed       int       `json:"tokens_used"`
	TimeBudgetSeconds int      `json:"time_budget_seconds"`
	TimeUsedSeconds  int       `json:"time_used_seconds"`
	Status           string    `json:"status"`
}

// GoalStore persists goals per session in PostgreSQL.
type GoalStore struct {
	db *sql.DB
}

// NewGoalStore returns a GoalStore backed by the given *sql.DB.
func NewGoalStore(db *sql.DB) (*GoalStore, error) {
	gs := &GoalStore{db: db}
	if err := gs.migrate(); err != nil {
		return nil, fmt.Errorf("goal: migrate: %w", err)
	}
	return gs, nil
}

func (gs *GoalStore) migrate() error {
	_, err := gs.db.Exec(`
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
	`)
	if err != nil {
		return err
	}
	// Add columns that may not exist in existing tables (idempotent).
	for _, col := range []string{
		`ALTER TABLE goals ADD COLUMN IF NOT EXISTS token_budget INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE goals ADD COLUMN IF NOT EXISTS tokens_used INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE goals ADD COLUMN IF NOT EXISTS time_budget_seconds INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE goals ADD COLUMN IF NOT EXISTS time_used_seconds INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE goals ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT ''`,
	} {
		if _, err := gs.db.Exec(col); err != nil {
			return err
		}
	}
	return nil
}

// Set creates or updates the goal for a session.
func (gs *GoalStore) Set(ctx context.Context, sessionID, text string) error {
	return gs.SetWithBudgets(ctx, sessionID, text, 0, 0)
}

// SetWithBudgets creates or updates the goal with optional token/time budgets.
func (gs *GoalStore) SetWithBudgets(ctx context.Context, sessionID, text string, tokenBudget, timeBudget int) error {
	now := time.Now().UTC()

	// Check existing to preserve CreatedAt.
	var createdAt time.Time
	err := gs.db.QueryRowContext(ctx,
		`SELECT created_at FROM goals WHERE session_id = $1`, sessionID,
	).Scan(&createdAt)
	if err == nil {
		// existing goal, preserve CreatedAt
	} else if err == sql.ErrNoRows {
		createdAt = now
	} else {
		return fmt.Errorf("goal: checking existing: %w", err)
	}

	_, err = gs.db.ExecContext(ctx, `
		INSERT INTO goals (session_id, text, active, created_at, updated_at,
			token_budget, tokens_used, time_budget_seconds, time_used_seconds, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (session_id) DO UPDATE SET
			text = EXCLUDED.text,
			active = EXCLUDED.active,
			updated_at = EXCLUDED.updated_at,
			token_budget = COALESCE(NULLIF(EXCLUDED.token_budget, 0), goals.token_budget),
			tokens_used = goals.tokens_used,
			time_budget_seconds = COALESCE(NULLIF(EXCLUDED.time_budget_seconds, 0), goals.time_budget_seconds),
			time_used_seconds = goals.time_used_seconds,
			status = COALESCE(NULLIF(EXCLUDED.status, ''), goals.status)
	`, sessionID, text, true, createdAt, now, tokenBudget, 0, timeBudget, 0, "")
	if err != nil {
		return fmt.Errorf("goal: set: %w", err)
	}
	return nil
}

// Get returns the current goal for a session, or nil if none exists.
func (gs *GoalStore) Get(ctx context.Context, sessionID string) (*Goal, error) {
	var g Goal
	err := gs.db.QueryRowContext(ctx,
		`SELECT text, active, created_at, updated_at,
			token_budget, tokens_used, time_budget_seconds, time_used_seconds, status
		 FROM goals WHERE session_id = $1`, sessionID,
	).Scan(&g.Text, &g.Active, &g.CreatedAt, &g.UpdatedAt,
		&g.TokenBudget, &g.TokensUsed, &g.TimeBudgetSeconds, &g.TimeUsedSeconds, &g.Status)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("goal: get: %w", err)
	}
	return &g, nil
}

// Pause sets the goal's active flag to false.
func (gs *GoalStore) Pause(ctx context.Context, sessionID string) error {
	return gs.setActive(ctx, sessionID, false)
}

// Resume sets the goal's active flag to true.
func (gs *GoalStore) Resume(ctx context.Context, sessionID string) error {
	return gs.setActive(ctx, sessionID, true)
}

// SetStatus updates the goal's status field.
func (gs *GoalStore) SetStatus(ctx context.Context, sessionID, status string) error {
	now := time.Now().UTC()
	result, err := gs.db.ExecContext(ctx,
		`UPDATE goals SET status = $1, updated_at = $2 WHERE session_id = $3`,
		status, now, sessionID)
	if err != nil {
		return fmt.Errorf("goal: set status: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("goal: no goal set for session %s", sessionID)
	}
	return nil
}

// UpdateUsageAtomic increments the token and time usage counters atomically
// and returns the new totals. Also reports whether the token budget has been
// exceeded after the increment. This avoids the TOCTOU race between a
// separate Get() → check → UpdateUsage() call chain.
func (gs *GoalStore) UpdateUsageAtomic(ctx context.Context, sessionID string, tokens int, seconds int) (newTokens int, newSeconds int, budgetExceeded bool, err error) {
	now := time.Now().UTC()
	var tokenBudget int
	err = gs.db.QueryRowContext(ctx,
		`UPDATE goals SET tokens_used = tokens_used + $1,
			time_used_seconds = time_used_seconds + $2,
			updated_at = $3
		 WHERE session_id = $4
		 RETURNING tokens_used, time_used_seconds, token_budget`,
		tokens, seconds, now, sessionID,
	).Scan(&newTokens, &newSeconds, &tokenBudget)
	if err != nil {
		if err == sql.ErrNoRows {
			return 0, 0, false, fmt.Errorf("goal: no goal set for session %s", sessionID)
		}
		return 0, 0, false, fmt.Errorf("goal: atomic update usage: %w", err)
	}
	budgetExceeded = tokenBudget > 0 && newTokens > tokenBudget
	return newTokens, newSeconds, budgetExceeded, nil
}

// Clear removes the goal for a session entirely.
func (gs *GoalStore) Clear(ctx context.Context, sessionID string) error {
	_, err := gs.db.ExecContext(ctx, `DELETE FROM goals WHERE session_id = $1`, sessionID)
	return err
}

// GetJSON returns the current goal marshaled as JSON, or nil if none exists.
// Used by tools to bridge the GoalStore → GoalQuerier interface.
func (gs *GoalStore) GetJSON(ctx context.Context, sessionID string) (json.RawMessage, error) {
	g, err := gs.Get(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if g == nil {
		return nil, nil
	}
	b, err := json.Marshal(g)
	if err != nil {
		return nil, fmt.Errorf("goal: marshal: %w", err)
	}
	return b, nil
}

func (gs *GoalStore) setActive(ctx context.Context, sessionID string, active bool) error {
	now := time.Now().UTC()
	result, err := gs.db.ExecContext(ctx,
		`UPDATE goals SET active = $1, updated_at = $2 WHERE session_id = $3`,
		active, now, sessionID)
	if err != nil {
		return fmt.Errorf("goal: set active: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return fmt.Errorf("goal: no goal set for session %s", sessionID)
	}
	return nil
}

package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/dbschema"
	_ "github.com/lib/pq"
)

// Goal is a standing objective that is injected into the system prompt
// every turn until paused or cleared.
type Goal struct {
	ID                  string    `json:"id"`
	TimeUsedNanoseconds int64     `json:"time_used_nanoseconds"`
	Text                string    `json:"text"`
	Active              bool      `json:"active"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
	TokenBudget         int       `json:"token_budget"`
	TokensUsed          int       `json:"tokens_used"`
	TimeBudgetSeconds   int       `json:"time_budget_seconds"`
	TimeUsedSeconds     int       `json:"time_used_seconds"`
	Status              string    `json:"status"`
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
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return dbschema.Goals(ctx, gs.db)
}

// Set creates or updates the goal for a session.
func (gs *GoalStore) Set(ctx context.Context, sessionID, text string) error {
	return gs.SetWithBudgets(ctx, sessionID, text, 0, 0)
}

// SetWithBudgets creates or updates the goal with optional token/time budgets.
func (gs *GoalStore) SetWithBudgets(ctx context.Context, sessionID, text string, tokenBudget, timeBudget int) error {
	if tokenBudget < 0 || timeBudget < 0 {
		return fmt.Errorf("goal: budgets must be non-negative")
	}
	now := time.Now().UTC()

	// Check existing to preserve CreatedAt.
	var createdAt time.Time
	err := gs.executor(ctx).QueryRowContext(ctx,
		`SELECT created_at FROM goals WHERE session_id = $1`, sessionID,
	).Scan(&createdAt)
	if err == nil {
		// existing goal, preserve CreatedAt
	} else if err == sql.ErrNoRows {
		createdAt = now
	} else {
		return fmt.Errorf("goal: checking existing: %w", err)
	}

	_, err = gs.executor(ctx).ExecContext(ctx, `
		INSERT INTO goals (session_id, text, active, created_at, updated_at,
			token_budget, tokens_used, time_budget_seconds, time_used_seconds, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (session_id) DO UPDATE SET
			text = EXCLUDED.text,
			active = EXCLUDED.active,
			updated_at = EXCLUDED.updated_at,
            goal_id = CASE WHEN goals.text = EXCLUDED.text THEN goals.goal_id ELSE EXCLUDED.goal_id END,
            token_budget = CASE WHEN goals.text = EXCLUDED.text THEN COALESCE(NULLIF(EXCLUDED.token_budget, 0), goals.token_budget) ELSE EXCLUDED.token_budget END,
            tokens_used = CASE WHEN goals.text = EXCLUDED.text THEN goals.tokens_used ELSE 0 END,
            time_budget_seconds = CASE WHEN goals.text = EXCLUDED.text THEN COALESCE(NULLIF(EXCLUDED.time_budget_seconds, 0), goals.time_budget_seconds) ELSE EXCLUDED.time_budget_seconds END,
            time_used_seconds = CASE WHEN goals.text = EXCLUDED.text THEN goals.time_used_seconds ELSE 0 END,
            time_used_nanoseconds = CASE WHEN goals.text = EXCLUDED.text THEN goals.time_used_nanoseconds ELSE 0 END,
            status = CASE WHEN goals.text = EXCLUDED.text THEN goals.status ELSE '' END
	`, sessionID, text, true, createdAt, now, tokenBudget, 0, timeBudget, 0, "")
	if err != nil {
		return fmt.Errorf("goal: set: %w", err)
	}
	return nil
}

// Get returns the current goal for a session, or nil if none exists.
func (gs *GoalStore) Get(ctx context.Context, sessionID string) (*Goal, error) {
	return readGoal(ctx, gs.executor(ctx), sessionID)
}

type goalReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func readGoal(ctx context.Context, reader goalReader, sessionID string) (*Goal, error) {
	var g Goal
	err := reader.QueryRowContext(ctx,
		`SELECT goal_id, time_used_nanoseconds, text, active, created_at, updated_at,
			token_budget, tokens_used, time_budget_seconds, time_used_seconds, status
		 FROM goals WHERE session_id = $1`, sessionID,
	).Scan(&g.ID, &g.TimeUsedNanoseconds, &g.Text, &g.Active, &g.CreatedAt, &g.UpdatedAt,
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
	result, err := gs.executor(ctx).ExecContext(ctx,
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

// UpdateUsageAtomic increments actual usage and marks either exhausted budget in
// the same SQL update. A zero budget is unlimited; equality exhausts a budget.
func (gs *GoalStore) UpdateUsageAtomic(ctx context.Context, sessionID string, tokens int, seconds int) (newTokens int, newSeconds int, budgetExceeded bool, err error) {
	if tokens < 0 || seconds < 0 {
		return 0, 0, false, fmt.Errorf("goal: negative usage delta")
	}
	err = gs.executor(ctx).QueryRowContext(ctx, `UPDATE goals SET
        tokens_used = tokens_used + $1,
        time_used_nanoseconds = time_used_nanoseconds + $2,
        time_used_seconds = (time_used_nanoseconds + $2) / 1000000000,
        status = CASE WHEN (token_budget > 0 AND tokens_used + $1 >= token_budget)
            OR (time_budget_seconds > 0 AND time_used_nanoseconds + $2 >= time_budget_seconds::bigint * 1000000000)
            THEN 'budget_limited' ELSE status END, updated_at = NOW()
        WHERE session_id = $3 RETURNING tokens_used, time_used_seconds, status = 'budget_limited'`,
		tokens, int64(seconds)*int64(time.Second), sessionID).Scan(&newTokens, &newSeconds, &budgetExceeded)
	if err != nil {
		err = fmt.Errorf("goal: atomic update usage: %w", err)
	}
	return
}

// Clear removes the goal for a session entirely.
func (gs *GoalStore) Clear(ctx context.Context, sessionID string) error {
	_, err := gs.executor(ctx).ExecContext(ctx, `DELETE FROM goals WHERE session_id = $1`, sessionID)
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
	result, err := gs.executor(ctx).ExecContext(ctx,
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

// Goal tools invoked within an admitted turn reuse its connection, so admission
// does not consume the last pool slot and deadlock goal reads/status updates.
type goalExecutor interface {
	goalReader
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func (gs *GoalStore) executor(ctx context.Context) goalExecutor {
	state, _ := ctx.Value(goalAccountingKey{}).(*GoalAccountingState)
	if state != nil && state.store != nil && state.store.db == gs.db {
		state.mu.Lock()
		lease := state.lease
		closed := state.closed
		state.mu.Unlock()
		if lease != nil && !closed {
			return lease
		}
	}
	return gs.db
}

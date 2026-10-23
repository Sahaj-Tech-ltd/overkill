package agent

import (
	"context"
	"crypto/rand"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/modelusage"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

var (
	ErrGoalBudget     = errors.New("agent: goal budget exhausted")
	ErrGoalTurnActive = errors.New("agent: another turn is already using this goal")
	ErrGoalChanged    = errors.New("agent: goal changed during the turn; start a new turn")
)

type goalAccountingKey struct{}

// GoalAccountingState belongs to one admitted turn, not to the Agent. Actual
// provider usage is incremental; persisted goal totals are never subtracted from
// per-turn totals. A durable turn ledger retains attribution after replacement.
type GoalAccountingState struct {
	mu             sync.Mutex
	store          *GoalStore
	sessionID      string
	goalID         string
	turnID         string
	lease          *sql.Conn
	accountedAt    time.Time
	pendingTokens  int
	reportedTokens int
	timer          *time.Timer
	cancel         context.CancelCauseFunc
	closed         bool
}

func NewGoalAccountingState(store *GoalStore, sessionID string) *GoalAccountingState {
	return &GoalAccountingState{store: store, sessionID: sessionID}
}

func (s *GoalAccountingState) RecordTurnStart(ctx context.Context) error {
	if s == nil || s.store == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.attachLocked(ctx)
}

func (s *GoalAccountingState) attachLocked(ctx context.Context) error {
	if s.goalID != "" || s.closed {
		return nil
	}
	g, err := readGoal(ctx, s.store.db, s.sessionID)
	if err != nil {
		return err
	}
	if g == nil || !g.Active || g.Status == "complete" || g.Status == "blocked" {
		return nil
	}
	conn, err := s.store.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("goal: admission connection: %w", err)
	}
	var admitted bool
	err = conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(hashtextextended(current_schema() || ':goal-turn:' || $1, 0))`, s.sessionID).Scan(&admitted)
	if err != nil || !admitted {
		conn.Close()
		if err != nil {
			return fmt.Errorf("goal: admission: %w", err)
		}
		return ErrGoalTurnActive
	}
	s.lease = conn
	// Re-read only after admission: another process may have spent the budget
	// between the initial lookup and obtaining the lock.
	g, err = readGoal(ctx, s.lease, s.sessionID)
	if err != nil {
		s.releaseLocked()
		return err
	}
	if g == nil || !g.Active || g.Status == "complete" || g.Status == "blocked" {
		s.releaseLocked()
		return nil
	}
	s.goalID = g.ID
	var id [16]byte
	if _, err = rand.Read(id[:]); err != nil {
		s.goalID = ""
		s.releaseLocked()
		return err
	}
	s.turnID = hex.EncodeToString(id[:])
	s.accountedAt = time.Now()
	admittedAt := s.accountedAt
	_, err = s.lease.ExecContext(ctx, `INSERT INTO goal_turn_usage (turn_id,goal_id,session_id) VALUES ($1,$2,$3)`, s.turnID, s.goalID, s.sessionID)
	if err != nil {
		s.goalID = ""
		s.turnID = ""
		s.releaseLocked()
		return fmt.Errorf("goal: record turn admission: %w", err)
	}
	if err = s.flushLocked(); err != nil {
		return err
	}
	if g.Status == "budget_limited" || (g.TokenBudget > 0 && g.TokensUsed >= g.TokenBudget) || (g.TimeBudgetSeconds > 0 && g.TimeUsedNanoseconds >= int64(g.TimeBudgetSeconds)*int64(time.Second)) {
		return ErrGoalBudget
	}
	if g.TimeBudgetSeconds > 0 && s.cancel != nil {
		remaining := time.Duration(g.TimeBudgetSeconds)*time.Second - time.Duration(g.TimeUsedNanoseconds) - time.Since(admittedAt)
		s.timer = time.AfterFunc(remaining, func() {
			// Cancel immediately, independently of database latency or the
			// accounting mutex; persistence remains bounded and detached.
			s.cancel(ErrGoalBudget)
			s.mu.Lock()
			defer s.mu.Unlock()
			if s.closed {
				return
			}
			// Cleanup retries a transient write failure before releasing admission.
			_ = s.flushLocked()
		})
	}
	return nil
}

// flushLocked uses a detached, bounded context so a cancelled provider/tool
// cannot discard usage it already reported. Nanoseconds carry fractions across
// arbitrarily short turns; public whole seconds are derived from the total.
func (s *GoalAccountingState) flushLocked() error {
	if s.goalID == "" {
		return nil
	}
	now := time.Now()
	elapsed := now.Sub(s.accountedAt).Nanoseconds()
	if elapsed < 0 {
		elapsed = 0
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	tx, err := s.lease.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("goal: usage transaction: %w", err)
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `UPDATE goal_turn_usage SET tokens_used=tokens_used+$1,
  time_used_nanoseconds=time_used_nanoseconds+$2 WHERE turn_id=$3`, s.pendingTokens, elapsed, s.turnID)
	if err == nil {
		_, err = tx.ExecContext(ctx, `UPDATE goals SET tokens_used=tokens_used+$1,
   time_used_nanoseconds=time_used_nanoseconds+$2,
   time_used_seconds=(time_used_nanoseconds+$2)/1000000000,
   status=CASE WHEN (token_budget>0 AND tokens_used+$1>=token_budget)
    OR (time_budget_seconds>0 AND time_used_nanoseconds+$2>=time_budget_seconds::bigint*1000000000)
    THEN 'budget_limited' ELSE status END, updated_at=NOW()
   WHERE session_id=$3 AND goal_id=$4`, s.pendingTokens, elapsed, s.sessionID, s.goalID)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		return fmt.Errorf("goal: persist actual usage: %w", err)
	}
	s.pendingTokens = 0
	s.accountedAt = now
	return nil
}

func (s *GoalAccountingState) Check(ctx context.Context) error {
	if s == nil || s.store == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return context.Canceled
	}
	if err := s.attachLocked(ctx); err != nil {
		return err
	}
	if s.goalID == "" {
		return nil
	}
	if err := s.flushLocked(); err != nil {
		return err
	}
	g, err := readGoal(ctx, s.lease, s.sessionID)
	if err != nil {
		return err
	}
	if g == nil || g.ID != s.goalID {
		return ErrGoalChanged
	}
	if g.Status == "budget_limited" {
		return ErrGoalBudget
	}
	if !g.Active || g.Status == "complete" || g.Status == "blocked" {
		return fmt.Errorf("agent: goal is no longer active")
	}
	return nil
}

func (s *GoalAccountingState) RecordUsage(_ context.Context, usage providers.Usage) error {
	if s == nil || s.store == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.goalID == "" || s.closed {
		return nil
	}
	// Cached input is already part of input usage, not additional tokens.
	if usage.InputTokens > 0 {
		s.pendingTokens += usage.InputTokens
		s.reportedTokens += usage.InputTokens
	}
	if usage.OutputTokens > 0 {
		s.pendingTokens += usage.OutputTokens
		s.reportedTokens += usage.OutputTokens
	}
	return s.flushLocked()
}

// RecordTurnEnd accepts a cumulative total for this turn (legacy callers),
// persists only the unreported delta, and releases admission exactly once.
func (s *GoalAccountingState) RecordTurnEnd(_ context.Context, tokensUsed int) error {
	if s == nil || s.store == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	if tokensUsed > s.reportedTokens {
		s.pendingTokens += tokensUsed - s.reportedTokens
		s.reportedTokens = tokensUsed
	}
	if s.timer != nil {
		s.timer.Stop()
	}
	err := s.flushLocked()
	if s.turnID != "" && s.lease != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_, endErr := s.lease.ExecContext(ctx, `UPDATE goal_turn_usage SET ended_at=NOW() WHERE turn_id=$1`, s.turnID)
		cancel()
		if err == nil {
			err = endErr
		}
	}
	s.closed = true
	s.releaseLocked()
	return err
}

func (s *GoalAccountingState) releaseLocked() {
	if s.lease == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	var released bool
	err := s.lease.QueryRowContext(ctx, `SELECT pg_advisory_unlock(hashtextextended(current_schema() || ':goal-turn:' || $1, 0))`, s.sessionID).Scan(&released)
	cancel()
	if err != nil || !released {
		_ = s.lease.Raw(func(any) error { return driver.ErrBadConn })
	}
	_ = s.lease.Close()
	s.lease = nil
}

func (a *Agent) beginGoalTurn(ctx context.Context) (context.Context, *GoalAccountingState, error) {
	if existing, _ := ctx.Value(goalAccountingKey{}).(*GoalAccountingState); existing != nil {
		return ctx, nil, nil
	}
	a.mu.RLock()
	store := a.goalStore
	a.mu.RUnlock()
	if store == nil {
		return ctx, nil, nil
	}
	ctx, cancel := context.WithCancelCause(ctx)
	state := NewGoalAccountingState(store, a.SessionID())
	state.cancel = cancel
	ctx = context.WithValue(ctx, goalAccountingKey{}, state)
	ctx = modelusage.WithCallbacks(ctx, checkGoalBudget, recordGoalUsage)
	if err := state.RecordTurnStart(ctx); err != nil {
		_ = state.RecordTurnEnd(ctx, 0)
		cancel(err)
		return ctx, nil, err
	}
	return ctx, state, nil
}

func checkGoalBudget(ctx context.Context) error {
	if err := modelusage.CheckAccounting(ctx); err != nil {
		return err
	}
	state, _ := ctx.Value(goalAccountingKey{}).(*GoalAccountingState)
	return state.Check(ctx)
}

func recordGoalUsage(ctx context.Context, usage providers.Usage) error {
	state, _ := ctx.Value(goalAccountingKey{}).(*GoalAccountingState)
	return errors.Join(state.RecordUsage(ctx, usage), modelusage.RecordAccounting(ctx, usage))
}

// Stream usage events are cumulative snapshots; some protocols send input and
// output separately. Keep each field's high-water mark, charge only increases,
// and never estimate missing usage from content or count cached input twice.
func mergeGoalStreamUsage(previous providers.Usage, next providers.Usage) (providers.Usage, providers.Usage) {
	merged := previous
	if next.InputTokens > merged.InputTokens {
		merged.InputTokens = next.InputTokens
	}
	if next.OutputTokens > merged.OutputTokens {
		merged.OutputTokens = next.OutputTokens
	}
	if next.CachedInputTokens > merged.CachedInputTokens {
		merged.CachedInputTokens = next.CachedInputTokens
	}
	return merged, providers.Usage{InputTokens: merged.InputTokens - previous.InputTokens, OutputTokens: merged.OutputTokens - previous.OutputTokens}
}

func recordProviderUsage(ctx context.Context, model string, usage providers.Usage, reported bool) error {
	return recordGoalUsage(modelusage.WithSample(ctx, model, reported), usage)
}

func usageResponseModel(requested string, response providers.Response) string {
	if response.Model != "" {
		return response.Model
	}
	return requested
}

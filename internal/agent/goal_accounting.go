package agent

import (
	"context"
	"time"
)

// GoalAccountingState tracks per-turn token delta and wall-clock time
// to drive goal budget enforcement.
type GoalAccountingState struct {
	store        *GoalStore
	sessionID    string
	turnStartAt  time.Time
	tokensAtStart int
	secondsAtStart int
}

// NewGoalAccountingState creates a state tracker for the given session.
// Pass nil store to make accounting a no-op.
func NewGoalAccountingState(store *GoalStore, sessionID string) *GoalAccountingState {
	return &GoalAccountingState{
		store:        store,
		sessionID:    sessionID,
	}
}

// RecordTurnStart snapshots the current usage counters at the start of a turn.
// Safe to call even with nil store (no-op).
func (s *GoalAccountingState) RecordTurnStart(ctx context.Context) {
	if s == nil || s.store == nil {
		return
	}
	g, err := s.store.Get(ctx, s.sessionID)
	if err != nil || g == nil {
		return
	}
	s.turnStartAt = time.Now()
	s.tokensAtStart = g.TokensUsed
	s.secondsAtStart = g.TimeUsedSeconds
}

// RecordTurnEnd computes the delta since RecordTurnStart, persists it via
// UpdateUsageAtomic (which returns new totals atomically), and checks budget
// thresholds inline — avoiding the TOCTOU race between a separate Get→check
// and a concurrent UpdateUsage from another turn.
//
// tokenUsed is the total tokens used so far this turn (cumulative). We compute
// the delta against the snapshot from RecordTurnStart.
//
// Safe to call even with nil store (no-op).
func (s *GoalAccountingState) RecordTurnEnd(ctx context.Context, tokensUsed int) {
	if s == nil || s.store == nil {
		return
	}

	deltaTokens := tokensUsed - s.tokensAtStart
	if deltaTokens < 0 {
		deltaTokens = 0
	}

	elapsed := int(time.Since(s.turnStartAt).Seconds())
	if elapsed < 0 {
		elapsed = 0
	}

	// Persist usage deltas atomically and check budget in the same query.
	if deltaTokens > 0 || elapsed > 0 {
		_, _, budgetExceeded, err := s.store.UpdateUsageAtomic(ctx, s.sessionID, deltaTokens, elapsed)
		if err != nil {
			return
		}
		if budgetExceeded {
			_ = s.store.SetStatus(ctx, s.sessionID, "budget_limited")
		}
	}
}

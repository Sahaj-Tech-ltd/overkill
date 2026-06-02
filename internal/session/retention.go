package session

import (
	"context"
)

// EnforceMax keeps at most `max` sessions in the store, deleting the
// oldest by UpdatedAt when the count exceeds the cap. max <= 0 means
// "no cap" — caller intends unlimited retention (the §4.6 default).
//
// Sub-sessions (those with a non-empty ParentID) are excluded from the
// count and never pruned by this policy — they're owned by their
// parent's lifecycle and pruning them out from under a running parent
// would corrupt the orchestration ledger.
//
// Returns the number of deleted sessions.
func EnforceMax(ctx context.Context, store Store, max int) (int, error) {
	if store == nil || max <= 0 {
		return 0, nil
	}
	// Fetch oldest-first so we prune the stalest sessions.
	// Limit: fetch generously in case some are sub-sessions (filtered below),
	// but cap to avoid unbounded loads on very large stores.
	limit := max * 5
	sessions, err := store.List(ctx, ListOptions{OldestFirst: true, Limit: limit})
	if err != nil {
		return 0, err
	}
	// Filter out sub-sessions; they're not first-class for retention.
	top := make([]*Session, 0, len(sessions))
	for _, s := range sessions {
		if s == nil || s.ParentID != "" {
			continue
		}
		top = append(top, s)
	}
	if len(top) <= max {
		return 0, nil
	}
	// Already oldest-first from the query — delete from the head.
	toDelete := len(top) - max
	deleted := 0
	for i := 0; i < toDelete; i++ {
		if err := store.Delete(ctx, top[i].ID); err != nil {
			return deleted, err
		}
		deleted++
	}
	return deleted, nil
}

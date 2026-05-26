package session

import (
	"context"
	"sort"
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
	sessions, err := store.List(ctx, ListOptions{})
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
	// Oldest first so we delete from the head until the count fits.
	sort.Slice(top, func(i, j int) bool {
		return top[i].UpdatedAt.Before(top[j].UpdatedAt)
	})
	toDelete := len(top) - max
	deleted := 0
	for i := 0; i < toDelete; i++ {
		if err := store.Delete(ctx, top[i].ID); err != nil {
			// Surface the first error but keep trying — best-effort
			// retention shouldn't leave the user in a half-pruned state
			// just because one delete tripped a lock.
			return deleted, err
		}
		deleted++
	}
	return deleted, nil
}

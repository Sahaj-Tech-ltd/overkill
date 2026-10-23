package coordination

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestWorkerLeaseHasOneOwnerAcrossConcurrentStores(t *testing.T) {
	store, _, backend, _ := fixture(t)
	other, err := NewStore(t.Context(), store.db)
	require.NoError(t, err)
	const count = 16
	type result struct {
		lease Lease
		err   error
	}
	results := make(chan result, count)
	var workers sync.WaitGroup
	for i := range count {
		workers.Go(func() {
			candidate := store
			if i%2 == 1 {
				candidate = other
			}
			lease, err := candidate.AcquireLease(t.Context(), backend.ID, uuid.NewString(), time.Second)
			results <- result{lease, err}
		})
	}
	workers.Wait()
	close(results)
	var winner Lease
	for result := range results {
		if result.err == nil {
			require.Empty(t, winner.OwnerID)
			winner = result.lease
		} else {
			require.ErrorIs(t, result.err, ErrBusy)
		}
	}
	require.NotEmpty(t, winner.OwnerID)
	_, err = other.AcquireLease(t.Context(), backend.ID, winner.OwnerID, time.Second)
	require.ErrorIs(t, err, ErrBusy, "same owner cannot accidentally start a second executor")
	renewed, err := store.RenewLease(t.Context(), winner, 2*time.Second)
	require.NoError(t, err)
	require.True(t, renewed.ExpiresAt.After(winner.ExpiresAt))
	require.NoError(t, store.ReleaseLease(t.Context(), winner))
	newOwner, err := other.AcquireLease(t.Context(), backend.ID, uuid.NewString(), time.Second)
	require.NoError(t, err)
	require.NoError(t, store.ReleaseLease(t.Context(), winner), "old owner's cleanup must not remove replacement")
	_, err = other.RenewLease(t.Context(), newOwner, time.Second)
	require.NoError(t, err)
}

func TestExpiredWorkerRequiresReviewAndCannotCommitLateResult(t *testing.T) {
	store, frontend, backend, _ := fixture(t)
	ctx := t.Context()
	first, err := store.Send(ctx, frontend.ID, request("first"))
	require.NoError(t, err)
	lease, err := store.AcquireLease(ctx, backend.ID, uuid.NewString(), time.Second)
	require.NoError(t, err)
	claimed, err := store.ClaimLease(ctx, lease)
	require.NoError(t, err)
	require.Equal(t, first.ID, claimed.ID)
	second, err := store.Send(ctx, frontend.ID, request("second"))
	require.NoError(t, err)
	_, err = store.ClaimLease(ctx, lease)
	require.ErrorIs(t, err, ErrBusy)
	_, err = store.db.ExecContext(ctx, `UPDATE coordination_leases SET expires_at=clock_timestamp()-interval '1 second' WHERE peer_id=$1`, backend.ID)
	require.NoError(t, err)
	_, err = store.RenewLease(ctx, lease, time.Second)
	require.ErrorIs(t, err, ErrLeaseLost)
	_, err = store.WorkerTransition(ctx, lease, first.ID, claimed.Revision, "completed", json.RawMessage(`{"late":true}`))
	require.ErrorIs(t, err, ErrLeaseLost)
	restarted, err := NewStore(ctx, store.db)
	require.NoError(t, err)
	_, err = restarted.AcquireLease(ctx, backend.ID, uuid.NewString(), time.Second)
	require.ErrorIs(t, err, ErrRecoveryRequired)
	interrupted, err := store.Read(ctx, frontend.ID, first.ID)
	require.NoError(t, err)
	require.Equal(t, Blocked, interrupted.State)
	_, err = restarted.AcquireLease(ctx, backend.ID, uuid.NewString(), time.Second)
	require.ErrorIs(t, err, ErrRecoveryRequired)
	events, err := store.MessageEvents(ctx, frontend.ID, first.ID, 0, 10)
	require.NoError(t, err)
	require.Len(t, events, 3, "recovery emits one durable blocked event, never replays execution")
	queued, err := store.Read(ctx, frontend.ID, second.ID)
	require.NoError(t, err)
	require.Equal(t, Accepted, queued.State)
	_, err = store.Transition(ctx, frontend.ID, first.ID, interrupted.Revision, "cancelled", json.RawMessage(`{"reviewed":true}`))
	require.NoError(t, err)
	newLease, err := restarted.AcquireLease(ctx, backend.ID, uuid.NewString(), time.Second)
	require.NoError(t, err)
	next, err := restarted.ClaimLease(ctx, newLease)
	require.NoError(t, err)
	require.Equal(t, second.ID, next.ID)
}

func TestWorkerLeaseFencesForgedOwnersAndCancelledWork(t *testing.T) {
	store, frontend, backend, _ := fixture(t)
	ctx := t.Context()
	message, err := store.Send(ctx, frontend.ID, request("cancel"))
	require.NoError(t, err)
	lease, err := store.AcquireLease(ctx, backend.ID, uuid.NewString(), time.Second)
	require.NoError(t, err)
	for _, forged := range []Lease{{PeerID: backend.ID}, {PeerID: backend.ID, OwnerID: uuid.NewString()}} {
		_, err := store.ClaimLease(ctx, forged)
		require.ErrorIs(t, err, ErrLeaseLost)
		_, err = store.WorkerTransition(ctx, forged, message.ID, message.Revision, "cancelled", json.RawMessage(`{}`))
		require.ErrorIs(t, err, ErrLeaseLost)
	}
	claimed, err := store.ClaimLease(ctx, lease)
	require.NoError(t, err)
	_, err = store.Transition(ctx, frontend.ID, message.ID, claimed.Revision, "cancelled", json.RawMessage(`{}`))
	require.NoError(t, err)
	current, err := store.Read(ctx, backend.ID, message.ID)
	require.NoError(t, err)
	_, err = store.WorkerTransition(ctx, lease, message.ID, current.Revision, "completed", json.RawMessage(`{}`))
	require.Error(t, err)
	require.False(t, errors.Is(err, ErrConflict), "current revision cannot undo terminal cancellation")
}

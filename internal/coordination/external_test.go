package coordination

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestExternalRecoveryWaitsForPendingRenewalAndPreservesLiveWork(t *testing.T) {
	store, front, back, _ := fixture(t)
	message, err := store.Send(t.Context(), front.ID, request("manual-renew-race"))
	require.NoError(t, err)
	lease, err := store.AcquireLease(t.Context(), back.ID, uuid.NewString(), time.Minute)
	require.NoError(t, err)
	claimed, err := store.ClaimLease(t.Context(), lease)
	require.NoError(t, err)
	require.Equal(t, message.ID, claimed.ID)
	_, err = store.db.ExecContext(t.Context(), `UPDATE coordination_leases SET expires_at=clock_timestamp()-interval '1 second' WHERE peer_id=$1`, back.ID)
	require.NoError(t, err)
	tx, err := store.db.BeginTx(t.Context(), nil)
	require.NoError(t, err)
	defer tx.Rollback()
	_, err = tx.ExecContext(t.Context(), `UPDATE coordination_leases SET expires_at=clock_timestamp()+interval '1 minute' WHERE peer_id=$1`, back.ID)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- store.RecoverExternal(ctx, back.ID) }()
	require.Eventually(t, func() bool {
		var count int
		e := store.db.QueryRowContext(t.Context(), `SELECT count(*) FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE 'SELECT expires_at>clock_timestamp()%coordination_leases%'`).Scan(&count)
		return e == nil && count > 0
	}, time.Second, 10*time.Millisecond)
	select {
	case e := <-result:
		t.Fatalf("recovery bypassed pending renewal: %v", e)
	default:
	}
	require.NoError(t, tx.Commit())
	require.ErrorIs(t, <-result, ErrBusy)
	current, err := store.Read(t.Context(), front.ID, message.ID)
	require.NoError(t, err)
	require.Equal(t, Started, current.State)
	require.Equal(t, claimed.Revision, current.Revision)
	require.NoError(t, store.ReleaseLease(t.Context(), lease))
}

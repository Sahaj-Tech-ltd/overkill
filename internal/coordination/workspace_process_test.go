package coordination

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceProcessReservationOutlivesCallsAndBlocksConflictingEffects(t *testing.T) {
	s, front, back, _ := fixture(t)
	other, _, err := s.RegisterPeer(t.Context(), Peer{ProjectID: front.ProjectID, Name: "other", Role: "competing writer", Workspace: front.Workspace})
	require.NoError(t, err)
	lease, err := s.ReserveWorkspaceProcess(t.Context(), front.ID, uuid.NewString())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, lease.Release(context.Background())) })
	invoke := func(context.Context) error { return nil }
	require.NoError(t, s.WithOwnedPaths(t.Context(), front.ID, []string{"file.txt"}, invoke))
	require.ErrorIs(t, s.WithOwnedPaths(t.Context(), other.ID, []string{"file.txt"}, invoke), ErrOwnershipConflict)
	require.ErrorIs(t, s.WithUnrestrictedWorkspace(t.Context(), other.ID, invoke), ErrOwnershipConflict)
	require.NoError(t, s.WithUnrestrictedWorkspace(t.Context(), back.ID, invoke), "disjoint workspace remains usable")
	_, err = s.CreateContract(t.Context(), back.ID, contractSpec(back, "new-api"))
	require.ErrorIs(t, err, ErrOwnershipConflict, "agreements cannot change under background processes")
	_, err = s.ListContracts(t.Context(), back.ID)
	require.NoError(t, err, "reading agreements must remain responsive")
	restarted, err := NewStore(t.Context(), s.db)
	require.NoError(t, err)
	_, err = restarted.ReserveWorkspaceProcess(t.Context(), other.ID, uuid.NewString())
	require.ErrorIs(t, err, ErrOwnershipConflict, "new broker cannot assume an old process stopped")
	require.NoError(t, s.DisablePeer(t.Context(), front.ID))
	require.Error(t, lease.Check(t.Context()), "disabled owner loses process authority")
	require.ErrorIs(t, restarted.WithUnrestrictedWorkspace(t.Context(), other.ID, invoke), ErrOwnershipConflict, "disable does not prove termination")
	require.NoError(t, lease.Release(t.Context()), "owner cleanup must work after disable")
	require.NoError(t, restarted.WithUnrestrictedWorkspace(t.Context(), other.ID, invoke))
}

func TestWorkspaceProcessRetainsRequestLineageUntilCancelled(t *testing.T) {
	s, front, back, _ := fixture(t)
	_, err := s.Send(t.Context(), front.ID, request("terminal-request"))
	require.NoError(t, err)
	worker, err := s.AcquireLease(t.Context(), back.ID, uuid.NewString(), 30*time.Second)
	require.NoError(t, err)
	m, err := s.ClaimLease(t.Context(), worker)
	require.NoError(t, err)
	launch, cancel := context.WithCancel(withExecutionLineage(t.Context(), m, worker))
	reservation, err := s.ReserveWorkspaceProcess(launch, back.ID, uuid.NewString())
	require.NoError(t, err)
	defer reservation.Release(context.Background())
	cancel()
	require.NoError(t, reservation.Check(t.Context()), "launch RPC ending does not revoke an active request")
	_, err = s.Transition(t.Context(), front.ID, m.ID, m.Revision, "cancelled", json.RawMessage(`{"reason":"user stopped"}`))
	require.NoError(t, err)
	require.ErrorIs(t, reservation.Check(t.Context()), ErrConflict, "request cancellation invalidates retained process authority")
}

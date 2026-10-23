package agent

import (
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"sync"
	"testing"
)

func TestObservedRuntimePersistenceSnapshotsAndRecoveryAreIndependent(t *testing.T) {
	a := goalAgent(&goalTestProvider{}, nil, "persisted-observer")
	defer a.Shutdown()
	var mu sync.Mutex
	var snapshots []RuntimeStatusSnapshot
	require.NoError(t, a.SetRuntimeStatusPersistence(nil, func(_ context.Context, s RuntimeStatusSnapshot) error {
		mu.Lock()
		snapshots = append(snapshots, s)
		mu.Unlock()
		return nil
	}))
	done := StartVerification(a.observedVerificationContext(t.Context()), "fixture checker", "source.json")
	inFlight := a.RuntimeStatusSnapshot()
	require.True(t, inFlight.Verification.Running)
	recovered := RecoverRuntimeStatus(inFlight)
	require.False(t, recovered.Verification.Running)
	require.Equal(t, 1, recovered.Verification.Unconfirmed)
	require.True(t, inFlight.Verification.Running)
	require.Equal(t, "running", inFlight.Verification.Tests[0].Status)
	done("passed", "")
	assessment := a.assessTurnConfidence("explain this fixture")
	require.NotNil(t, assessment)
	snapshot := a.RuntimeStatusSnapshot()
	require.Equal(t, "assessed", snapshot.Evaluation.Status)
	require.Equal(t, assessment.Score, *snapshot.Evaluation.Score)
	require.Equal(t, 1, snapshot.Verification.Passed)
	snapshot.Evaluation.Reasoning = "mutated"
	*snapshot.Evaluation.Score = 0
	require.NotEqual(t, "mutated", a.RuntimeStatusSnapshot().Evaluation.Reasoning)
	mu.Lock()
	require.GreaterOrEqual(t, len(snapshots), 4)
	restored := snapshots[len(snapshots)-1]
	mu.Unlock()
	other := goalAgent(&goalTestProvider{}, nil, "persisted-observer")
	defer other.Shutdown()
	require.NoError(t, other.SetRuntimeStatusPersistence(&restored, nil))
	before := other.VerificationSnapshot().Tests[0].ID
	next := StartVerification(other.observedVerificationContext(t.Context()), "new process checker", "source.json")
	next("failed", "real failure")
	checks := other.VerificationSnapshot()
	require.Len(t, checks.Tests, 2)
	require.NotEqual(t, before, checks.Tests[1].ID)
	foreign := restored
	foreign.SessionID = "foreign"
	require.Error(t, other.SetRuntimeStatusPersistence(&foreign, nil))
}
func TestRuntimeStatusPersistenceFailureIsVisibleWithoutFalsifyingChecks(t *testing.T) {
	a := goalAgent(&goalTestProvider{}, nil, "failed-observer")
	defer a.Shutdown()
	require.NoError(t, a.SetRuntimeStatusPersistence(nil, func(context.Context, RuntimeStatusSnapshot) error {
		return errors.New("controlled database unavailable")
	}))
	done := StartVerification(a.observedVerificationContext(t.Context()), "real checker", "source.json")
	done("failed", "verification failed")
	snapshot := a.RuntimeStatusSnapshot()
	require.True(t, snapshot.PersistenceAvailable)
	require.True(t, snapshot.PersistenceFailed)
	require.Equal(t, 1, snapshot.Verification.Failed)
	require.Zero(t, snapshot.Verification.Passed)
}

package agent

import (
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func TestVerificationObservationsBoundedAndSnapshotsIndependent(t *testing.T) {
	a := goalAgent(&goalTestProvider{}, nil, "bounded-verification")
	defer a.Shutdown()
	ctx := a.observedVerificationContext(t.Context())
	for i := 0; i < 105; i++ {
		done := StartVerification(ctx, "fixture checker", "file.json")
		done("passed", "")
		done("failed", "duplicate finish ignored")
	}
	snap := a.VerificationSnapshot()
	require.Len(t, snap.Tests, 100)
	require.Equal(t, 100, snap.Total)
	require.Equal(t, 100, snap.Passed)
	require.Zero(t, snap.Failed)
	require.Equal(t, 5, snap.Dropped)
	snap.Tests[0].Name = "mutated snapshot"
	require.NotEqual(t, "mutated snapshot", a.VerificationSnapshot().Tests[0].Name)
	done := StartVerification(ctx, "canceled checker", "file.json")
	require.True(t, a.VerificationSnapshot().Running)
	done("canceled", "context canceled")
	snap = a.VerificationSnapshot()
	require.False(t, snap.Running)
	require.Equal(t, 99, snap.Passed)
	require.Equal(t, 1, snap.Canceled)
	require.Zero(t, snap.Failed)
}

func TestVerificationObservationDisplayTextIsBounded(t *testing.T) {
	a := goalAgent(&goalTestProvider{}, nil, "bounded-output")
	defer a.Shutdown()
	ctx := a.observedVerificationContext(t.Context())
	done := StartVerification(ctx, strings.Repeat("name", 300), strings.Repeat("path", 2000))
	done("failed", strings.Repeat("é", 600000))
	snap := a.VerificationSnapshot()
	require.Len(t, snap.Tests, 1)
	check := snap.Tests[0]
	require.LessOrEqual(t, len(check.Name), 512)
	require.LessOrEqual(t, len(check.File), 4096)
	require.LessOrEqual(t, len(check.Error), 8192)
	require.True(t, check.OutputTruncated)
}

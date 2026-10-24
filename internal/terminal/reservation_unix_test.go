//go:build !windows

package terminal

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type testReservation struct{ allowed, released atomic.Bool }

func (r *testReservation) Check(context.Context) error {
	if !r.allowed.Load() {
		return errors.New("revoked")
	}
	return nil
}
func (r *testReservation) Release(context.Context) error { r.released.Store(true); return nil }

func TestReservationDenialPreventsProcessAndRevocationClosesBeforeRelease(t *testing.T) {
	workspace := t.TempDir()
	denied, err := New(workspace, Options{Reserve: func(context.Context, Owner, string) (Reservation, error) {
		return nil, errors.New("conflicting writer")
	}})
	require.NoError(t, err)
	defer denied.Close()
	owner := Owner{Session: "chat", Worker: "worker"}
	_, err = denied.Start(t.Context(), owner, "touch must-not-exist", "", 24, 80)
	require.ErrorContains(t, err, "conflicting writer")
	_, err = os.Stat(filepath.Join(workspace, "must-not-exist"))
	require.True(t, os.IsNotExist(err))
	reservation := &testReservation{}
	reservation.allowed.Store(true)
	m, err := New(workspace, Options{Reserve: func(context.Context, Owner, string) (Reservation, error) { return reservation, nil }})
	require.NoError(t, err)
	defer m.Close()
	i, err := m.Start(t.Context(), owner, "cat", "", 24, 80)
	require.NoError(t, err)
	require.False(t, reservation.released.Load())
	reservation.allowed.Store(false)
	_, err = m.Write(t.Context(), owner, i.ID, false, []byte("must not arrive\n"))
	require.ErrorContains(t, err, "authority lost")
	require.Eventually(t, func() bool { return reservation.released.Load() }, time.Second, 10*time.Millisecond)
	require.NoError(t, m.CloseTerminal(owner, i.ID, true))
	chunk, err := m.Read(t.Context(), owner, i.ID, true, 0, 1024, 0)
	require.NoError(t, err)
	require.Equal(t, "closed", chunk.Info.State)
	require.NotContains(t, chunk.Output, "must not arrive")
}

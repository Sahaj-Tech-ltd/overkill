//go:build windows

package terminal

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type windowsReservation struct{ released atomic.Int32 }

func (*windowsReservation) Check(context.Context) error     { return nil }
func (r *windowsReservation) Release(context.Context) error { r.released.Add(1); return nil }

func windowsRead(t *testing.T, m *Manager, owner Owner, id string, predicate func(Chunk) bool) Chunk {
	t.Helper()
	var out Chunk
	require.Eventually(t, func() bool {
		var err error
		out, err = m.Read(t.Context(), owner, id, false, 0, 65536, 100*time.Millisecond)
		require.NoError(t, err)
		return predicate(out)
	}, 8*time.Second, 20*time.Millisecond)
	return out
}

func TestWindowsNativeTerminalControlRetriesAndHandoff(t *testing.T) {
	require.True(t, Supported(), "Windows native acceptance requires ConPTY")
	child, parent := Owner{Session: "win-chat", Worker: "child"}, Owner{Session: "win-chat", Worker: "main"}
	reservation := &windowsReservation{}
	var receipts []Handoff
	m, err := New(t.TempDir(), Options{ParentOwner: parent, RecordHandoff: func(_ context.Context, h Handoff) error { receipts = append(receipts, h); return nil }, Reserve: func(context.Context, Owner, string) (Reservation, error) { return reservation, nil }})
	require.NoError(t, err)
	defer func() { require.NoError(t, m.Close()) }()
	info, err := m.Start(t.Context(), child, `[Console]::WriteLine('WINDOWS_READY'); $value=[Console]::ReadLine(); [Console]::WriteLine('WINDOWS_INPUT:'+$value); [Console]::ReadLine() | Out-Null`, "", 24, 80)
	require.NoError(t, err)
	windowsRead(t, m, child, info.ID, func(c Chunk) bool { return strings.Contains(c.Output, "WINDOWS_READY") })
	_, err = m.Read(t.Context(), parent, info.ID, false, 0, 100, 0)
	require.ErrorIs(t, err, ErrNotFound)
	human := Owner{Session: child.Session, Worker: "human-client"}
	taken, err := m.TakeControlInfo(human, info.ID, true, 1, false)
	require.NoError(t, err)
	_, err = m.WriteInput(t.Context(), child, info.ID, false, taken.ControlRevision, 1, []byte("blocked\r"))
	require.ErrorIs(t, err, ErrHumanControl)
	written, err := m.WriteInput(t.Context(), human, info.ID, true, taken.ControlRevision, 1, []byte("WINDOWS_VALUE\r"))
	require.NoError(t, err)
	require.True(t, written.Success)
	duplicate, err := m.WriteInput(t.Context(), human, info.ID, true, taken.ControlRevision, 1, []byte("WINDOWS_VALUE\r"))
	require.NoError(t, err)
	require.Equal(t, written, duplicate)
	_, err = m.WriteInput(t.Context(), human, info.ID, true, taken.ControlRevision, 1, []byte("changed\r"))
	require.ErrorIs(t, err, ErrInputConflict)
	returned, err := m.TakeControlInfo(human, info.ID, true, taken.ControlRevision, true)
	require.NoError(t, err)
	_, err = m.WriteInput(t.Context(), child, info.ID, false, 1, 1, []byte("retired\r"))
	require.ErrorIs(t, err, ErrControlChanged)
	value, err := m.HandoffToParent(t.Context(), child, info.ID, false, returned.ControlRevision)
	require.NoError(t, err)
	require.True(t, value.Success)
	require.NoError(t, m.CloseOwner(child))
	out := windowsRead(t, m, parent, info.ID, func(c Chunk) bool { return strings.Contains(c.Output, "WINDOWS_INPUT:WINDOWS_VALUE") })
	require.Equal(t, 1, strings.Count(out.Output, "WINDOWS_INPUT:WINDOWS_VALUE"))
	require.Equal(t, "running", out.Info.State)
	require.Len(t, receipts, 2)
	require.Equal(t, "applied", receipts[1].State)
	require.NoError(t, m.CloseAt(parent, info.ID, false, value.Terminal.ControlRevision))
	require.Equal(t, int32(1), reservation.released.Load(), "release only follows confirmed owned job/process cleanup")
	require.ErrorIs(t, m.Forget(child, info.ID, false), ErrOwnershipTransferred)
	require.NoError(t, m.Forget(parent, info.ID, false))
}

func TestWindowsNativeTerminalInterruptStopsRealCommand(t *testing.T) {
	m, err := New(t.TempDir(), Options{})
	require.NoError(t, err)
	defer m.Close()
	owner := Owner{Session: "win-chat", Worker: "main"}
	human := Owner{Session: owner.Session, Worker: "human"}
	info, err := m.Start(t.Context(), owner, `[Console]::WriteLine('INTERRUPT_READY'); Start-Sleep -Seconds 30`, "", 24, 80)
	require.NoError(t, err)
	windowsRead(t, m, owner, info.ID, func(c Chunk) bool { return strings.Contains(c.Output, "INTERRUPT_READY") })
	taken, err := m.TakeControlInfo(human, info.ID, true, 1, false)
	require.NoError(t, err)
	written, err := m.WriteInput(t.Context(), human, info.ID, true, taken.ControlRevision, 1, []byte{3})
	require.NoError(t, err)
	require.True(t, written.Success)
	windowsRead(t, m, owner, info.ID, func(c Chunk) bool { return c.Info.State != "running" && c.Info.State != "closing" })
}

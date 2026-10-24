//go:build !windows

package terminal

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestInputReceiptsRejectDuplicatesConflictsAndRetiredControl(t *testing.T) {
	m, err := New(t.TempDir(), Options{})
	require.NoError(t, err)
	defer m.Close()
	agent := Owner{Session: "chat", Worker: "worker"}
	human := Owner{Session: "chat", Worker: "human-client"}
	info, err := m.Start(t.Context(), agent, "stty -echo; printf READY; while IFS= read -r line; do printf 'GOT:%s\\n' \"$line\"; done", "", 24, 80)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		chunk, _ := m.Read(t.Context(), agent, info.ID, false, 0, 4096, 20*time.Millisecond)
		return strings.Contains(chunk.Output, "READY")
	}, time.Second, 10*time.Millisecond)
	one, err := m.WriteInput(t.Context(), agent, info.ID, false, 1, 1, []byte("one\n"))
	require.NoError(t, err)
	require.True(t, one.Success)
	retry, err := m.WriteInput(t.Context(), agent, info.ID, false, 1, 1, []byte("one\n"))
	require.NoError(t, err)
	require.Equal(t, one, retry)
	_, err = m.WriteInput(t.Context(), agent, info.ID, false, 1, 1, []byte("different\n"))
	require.ErrorIs(t, err, ErrInputConflict)
	_, err = m.WriteInput(t.Context(), agent, info.ID, false, 1, 3, []byte("skip\n"))
	require.ErrorIs(t, err, ErrInputOrder)
	_, err = m.WriteInput(t.Context(), agent, info.ID, false, 1, 2, []byte("two\n"))
	require.NoError(t, err)
	_, err = m.WriteInput(t.Context(), agent, info.ID, false, 1, 1, []byte("one\n"))
	require.ErrorIs(t, err, ErrInputOrder, "expired receipts never re-execute")
	taken, err := m.TakeControlInfo(human, info.ID, true, 1, false)
	require.NoError(t, err)
	require.Equal(t, uint64(2), taken.ControlRevision)
	duplicate, err := m.TakeControlInfo(human, info.ID, true, 1, false)
	require.NoError(t, err)
	require.Equal(t, taken.ControlRevision, duplicate.ControlRevision)
	returned, err := m.TakeControlInfo(human, info.ID, true, 2, true)
	require.NoError(t, err)
	require.Equal(t, uint64(3), returned.ControlRevision)
	_, err = m.WriteInput(t.Context(), agent, info.ID, false, 1, 3, []byte("stale-after-human\n"))
	require.ErrorIs(t, err, ErrControlChanged)
	require.ErrorIs(t, m.CloseAt(agent, info.ID, false, 1), ErrControlChanged)
	require.ErrorIs(t, m.ResizeAt(agent, info.ID, false, 1, 40, 120), ErrControlChanged)
	_, err = m.WriteInput(t.Context(), agent, info.ID, false, 3, 1, []byte("fresh\n"))
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		chunk, _ := m.Read(t.Context(), agent, info.ID, false, 0, 4096, 20*time.Millisecond)
		return strings.Contains(chunk.Output, "GOT:fresh")
	}, time.Second, 10*time.Millisecond)
	chunk, err := m.Read(t.Context(), agent, info.ID, false, 0, 4096, 0)
	require.NoError(t, err)
	require.Equal(t, 1, strings.Count(chunk.Output, "GOT:one"))
	require.NotContains(t, chunk.Output, "different")
	require.NotContains(t, chunk.Output, "skip")
	require.NotContains(t, chunk.Output, "stale-after-human")
	_, err = m.TakeControlInfo(human, info.ID, true, 3, false)
	require.NoError(t, err)
	_, err = m.TakeControlInfo(human, info.ID, true, 2, true)
	require.ErrorIs(t, err, ErrControlChanged, "old control retries cannot override a later transition")
}

func TestUnknownInputOutcomeIsRetainedWithoutRetryingPTY(t *testing.T) {
	m, err := New(t.TempDir(), Options{})
	require.NoError(t, err)
	defer m.Close()
	owner := Owner{Session: "chat", Worker: "worker"}
	i, err := m.Start(t.Context(), owner, "stty raw -echo; printf READY; exec sleep 30", "", 24, 80)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		chunk, _ := m.Read(t.Context(), owner, i.ID, false, 0, 4096, 20*time.Millisecond)
		return strings.Contains(chunk.Output, "READY")
	}, time.Second, 10*time.Millisecond)
	bytes := []byte(strings.Repeat("x", 16384))
	var uncertain InputReceipt
	for seq := uint64(1); seq <= 8; seq++ {
		ctx, cancel := context.WithTimeout(t.Context(), 40*time.Millisecond)
		r, err := m.WriteInput(ctx, owner, i.ID, false, 1, seq, bytes)
		cancel()
		require.NoError(t, err)
		if r.Outcome == "unknown" {
			uncertain = r
			break
		}
	}
	require.Equal(t, "unknown", uncertain.Outcome)
	require.False(t, uncertain.Success)
	require.Contains(t, uncertain.Error, "Inspect effects")
	require.NoError(t, m.CloseTerminal(owner, i.ID, true))
	duplicate, err := m.WriteInput(t.Context(), owner, i.ID, false, uncertain.ControlRevision, uncertain.Sequence, bytes)
	require.NoError(t, err)
	require.Equal(t, uncertain, duplicate)
	_, err = m.WriteInput(t.Context(), owner, i.ID, false, 1, uncertain.Sequence+1, []byte("late"))
	require.ErrorIs(t, err, ErrClosed)
}

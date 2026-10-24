//go:build !windows

package terminal

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestHandoffFencesChildAndSurvivesRacingCleanup(t *testing.T) {
	child := Owner{Session: "chat", Worker: "child"}
	parent := Owner{Session: "chat", Worker: "main"}
	prepared, release := make(chan struct{}), make(chan struct{})
	var mu sync.Mutex
	var states []string
	m, err := New(t.TempDir(), Options{ParentOwner: parent, RecordHandoff: func(_ context.Context, h Handoff) error {
		mu.Lock()
		states = append(states, h.State)
		mu.Unlock()
		if h.State == "prepared" {
			close(prepared)
			<-release
		}
		return nil
	}})
	require.NoError(t, err)
	defer m.Close()
	i, err := m.Start(t.Context(), child, "cat", "", 24, 80)
	require.NoError(t, err)
	transferred := make(chan TransferResult, 1)
	failure := make(chan error, 1)
	go func() {
		value, err := m.HandoffToParent(t.Context(), child, i.ID, false, i.ControlRevision)
		transferred <- value
		failure <- err
	}()
	<-prepared
	cleanup := make(chan error, 1)
	go func() { cleanup <- m.CloseOwner(child) }()
	close(release)
	value := <-transferred
	require.NoError(t, <-failure)
	require.NoError(t, <-cleanup)
	require.True(t, value.Success)
	require.Equal(t, "applied", value.Handoff.State)
	require.Equal(t, parent, value.Terminal.Owner)
	require.Equal(t, i.StartedAt, value.Terminal.StartedAt)
	require.Equal(t, uint64(2), value.Terminal.ControlRevision)
	mu.Lock()
	require.Equal(t, []string{"prepared", "applied"}, states)
	mu.Unlock()
	_, err = m.Read(t.Context(), child, i.ID, false, 0, 1024, 0)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = m.WriteInput(t.Context(), child, i.ID, false, 1, 1, []byte("stale child\n"))
	require.ErrorIs(t, err, ErrNotFound)
	_, err = m.WriteInput(t.Context(), parent, i.ID, false, 1, 1, []byte("stale revision\n"))
	require.ErrorIs(t, err, ErrControlChanged)
	_, err = m.WriteInput(t.Context(), parent, i.ID, false, 2, 1, []byte("parent input\n"))
	require.NoError(t, err)
	again, err := m.HandoffToParent(t.Context(), child, i.ID, false, 1)
	require.NoError(t, err)
	require.Equal(t, value, again, "lost handoff response returns original receipt without another transfer")
	require.NoError(t, m.CloseOwner(child))
	require.Equal(t, "running", m.List(parent, false)[0].State)
	require.NoError(t, m.CloseOwner(parent))
	require.NotEqual(t, "running", m.List(parent, false)[0].State)
	require.ErrorIs(t, m.Forget(child, i.ID, false), ErrOwnershipTransferred)
	require.Len(t, m.List(parent, false), 1, "retired child cannot remove the parent's closed terminal history")
	require.NoError(t, m.Forget(parent, i.ID, false))
	require.Empty(t, m.List(parent, false))
}

func TestHandoffDoesNotResetLifetimeAndRequiresDurableAdmission(t *testing.T) {
	child := Owner{Session: "chat", Worker: "child"}
	parent := Owner{Session: "chat", Worker: "main"}
	m, err := New(t.TempDir(), Options{ParentOwner: parent, MaxLifetime: 200 * time.Millisecond, RecordHandoff: func(context.Context, Handoff) error { return nil }})
	require.NoError(t, err)
	defer m.Close()
	i, err := m.Start(t.Context(), child, "cat", "", 24, 80)
	require.NoError(t, err)
	value, err := m.HandoffToParent(t.Context(), child, i.ID, false, 1)
	require.NoError(t, err)
	require.True(t, value.Success)
	require.Eventually(t, func() bool { return m.List(parent, false)[0].State == "closed" }, time.Second, 10*time.Millisecond)
	denied, err := New(t.TempDir(), Options{ParentOwner: parent, RecordHandoff: func(context.Context, Handoff) error { return errors.New("journal denied") }})
	require.NoError(t, err)
	defer denied.Close()
	i, err = denied.Start(t.Context(), child, "cat", "", 24, 80)
	require.NoError(t, err)
	_, err = denied.HandoffToParent(t.Context(), child, i.ID, false, 1)
	require.ErrorContains(t, err, "intent not confirmed")
	require.Len(t, denied.List(child, false), 1)
	require.Empty(t, denied.List(parent, false))
	require.NoError(t, denied.CloseOwner(child))
}

func TestUnconfirmedHandoffReceiptRemainsVisibleToParent(t *testing.T) {
	child := Owner{Session: "chat", Worker: "child"}
	parent := Owner{Session: "chat", Worker: "main"}
	m, err := New(t.TempDir(), Options{ParentOwner: parent, RecordHandoff: func(_ context.Context, h Handoff) error {
		if h.State == "applied" {
			return errors.New("commit unknown")
		}
		return nil
	}})
	require.NoError(t, err)
	defer m.Close()
	i, err := m.Start(t.Context(), child, "cat", "", 24, 80)
	require.NoError(t, err)
	value, err := m.HandoffToParent(t.Context(), child, i.ID, false, 1)
	require.NoError(t, err)
	require.False(t, value.Success)
	require.Equal(t, "unconfirmed", value.Handoff.State)
	require.NoError(t, m.CloseOwner(child))
	require.Len(t, m.List(parent, false), 1)
	require.NoError(t, m.Stop())
	require.NotEqual(t, "running", m.List(parent, false)[0].State)
}

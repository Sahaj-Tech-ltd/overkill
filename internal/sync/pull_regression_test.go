package sync

import (
	"context"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/stretchr/testify/require"
)

func TestPullUnchangedContentPreservesVersionAndAvoidsConflicts(t *testing.T) {
	ctx := context.Background()
	backend, err := NewFileBackend(config.SyncFileConfig{Path: t.TempDir()})
	require.NoError(t, err)
	source, target := newTestStore(t), newTestStore(t)
	sess := session.NewSession(t.TempDir())
	sess.Title = "unchanged"
	require.NoError(t, source.Create(ctx, sess))
	err = NewManager(source, backend).PushOne(ctx, sess.ID)
	require.NoError(t, err)
	manager := NewManager(target, backend)
	require.NoError(t, manager.PullOne(ctx, sess.ID))
	before, err := target.Load(ctx, sess.ID)
	require.NoError(t, err)
	for range 3 {
		require.NoError(t, manager.PullOne(ctx, sess.ID))
	}
	after, err := target.Load(ctx, sess.ID)
	require.NoError(t, err)
	require.True(t, before.UpdatedAt.Equal(after.UpdatedAt))
	sessions, err := target.List(ctx, session.ListOptions{})
	require.NoError(t, err)
	require.Len(t, sessions, 1)
}

func TestPullRepeatedDivergentRevisionKeepsOneConflictCopy(t *testing.T) {
	ctx := context.Background()
	backend, err := NewFileBackend(config.SyncFileConfig{Path: t.TempDir()})
	require.NoError(t, err)
	store := newTestStore(t)
	remote := session.NewSession(t.TempDir())
	remote.Title = "remote revision"
	data, err := EncodeSession(remote)
	require.NoError(t, err)
	require.NoError(t, backend.Push(ctx, remote.ID, data, SessionMeta{ID: remote.ID}))
	local := conflictCopy(remote)
	local.Title = "local revision"
	require.NoError(t, store.Create(ctx, &local))
	manager := NewManager(store, backend)
	for range 3 {
		require.NoError(t, manager.PullOne(ctx, remote.ID))
	}
	sessions, err := store.List(ctx, session.ListOptions{})
	require.NoError(t, err)
	require.Len(t, sessions, 2)
	current, err := store.Load(ctx, remote.ID)
	require.NoError(t, err)
	require.Equal(t, "local revision", current.Title)
	require.True(t, local.UpdatedAt.Equal(current.UpdatedAt))
}

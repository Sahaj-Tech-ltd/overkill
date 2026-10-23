package sync

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
	"github.com/stretchr/testify/require"
)

type gatedAutoBackend struct {
	Backend
	entered chan struct{}
	release chan struct{}
	calls   atomic.Int32
}

func (b *gatedAutoBackend) Push(ctx context.Context, id string, data []byte, meta SessionMeta) error {
	if b.calls.Add(1) == 1 {
		close(b.entered)
		select {
		case <-b.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return b.Backend.Push(ctx, id, data, meta)
}
func queuedAutoFixture(t *testing.T) (*Manager, *gatedAutoBackend, *session.Session) {
	t.Helper()
	store := newTestStore(t)
	sess := session.NewSession(t.TempDir())
	sess.Title = "initial"
	require.NoError(t, store.Create(t.Context(), sess))
	file, err := NewFileBackend(config.SyncFileConfig{Path: t.TempDir()})
	require.NoError(t, err)
	backend := &gatedAutoBackend{Backend: file, entered: make(chan struct{}), release: make(chan struct{})}
	manager := NewManager(store, backend)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = manager.CloseAutoPush(ctx)
	})
	return manager, backend, sess
}
func TestAutoPushQueuesLatestTurnAndDrainsBeforeStoreClose(t *testing.T) {
	manager, backend, sess := queuedAutoFixture(t)
	cfg := &config.Config{Sync: config.SyncConfig{AutoPush: true}}
	failures := make(chan error, 4)
	AutoPushIfEnabled(cfg, manager, sess.ID, func(err error) { failures <- err })
	select {
	case <-backend.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first upload did not begin")
	}
	for _, title := range []string{"second completed turn", "last completed turn"} {
		sess.Title = title
		require.NoError(t, manager.store.Save(t.Context(), sess))
		AutoPushIfEnabled(cfg, manager, sess.ID, func(err error) { failures <- err })
	}
	close(backend.release)
	require.NoError(t, manager.CloseAutoPush(t.Context()))
	require.Empty(t, failures)
	require.EqualValues(t, 2, backend.calls.Load(), "queued turns should coalesce into one latest-state upload")
	data, meta, err := backend.Pull(t.Context(), sess.ID)
	require.NoError(t, err)
	got, err := DecodeSession(data)
	require.NoError(t, err)
	require.Equal(t, "last completed turn", got.Title)
	require.Equal(t, got.Title, meta.Title)
}
func TestAutoPushManagersDoNotSuppressOneAnother(t *testing.T) {
	cfg := &config.Config{Sync: config.SyncConfig{AutoPush: true}}
	for i := 0; i < 2; i++ {
		manager, backend, sess := queuedAutoFixture(t)
		AutoPushIfEnabled(cfg, manager, sess.ID, nil)
		select {
		case <-backend.entered:
		case <-time.After(5 * time.Second):
			t.Fatal("independent manager was suppressed")
		}
		defer close(backend.release)
	}
}
func TestAutoPushShutdownCancelsAndJoinsActiveUpload(t *testing.T) {
	manager, backend, sess := queuedAutoFixture(t)
	cfg := &config.Config{Sync: config.SyncConfig{AutoPush: true, PushTimeoutSec: 5}}
	failure := make(chan error, 1)
	AutoPushIfEnabled(cfg, manager, sess.ID, func(err error) { failure <- err })
	select {
	case <-backend.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("upload did not begin")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 80*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, manager.CloseAutoPush(ctx), context.DeadlineExceeded)
	require.ErrorIs(t, <-failure, context.Canceled)
	manager.autoMu.Lock()
	require.Nil(t, manager.autoDone, "shutdown must join the worker before store owner returns")
	manager.autoMu.Unlock()
}

func TestAutoPushUsesConfiguredUploadTimeout(t *testing.T) {
	manager, backend, sess := queuedAutoFixture(t)
	cfg := &config.Config{Sync: config.SyncConfig{AutoPush: true, PushTimeoutSec: 1}}
	failure := make(chan error, 1)
	AutoPushIfEnabled(cfg, manager, sess.ID, func(err error) { failure <- err })
	select {
	case <-backend.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("upload did not begin")
	}
	select {
	case err := <-failure:
		require.ErrorIs(t, err, context.DeadlineExceeded)
	case <-time.After(3 * time.Second):
		t.Fatal("configured upload timeout was ignored")
	}
	require.NoError(t, manager.CloseAutoPush(t.Context()))
}

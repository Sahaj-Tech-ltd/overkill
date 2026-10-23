package session

import (
	"context"
	"database/sql"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPostgresStoreInitializationFails(t *testing.T) {
	store, err := NewPostgresStore(nil)
	require.Error(t, err)
	require.Nil(t, store)
	database, err := sql.Open("postgres", "postgres://unused@localhost/unused?sslmode=disable")
	require.NoError(t, err)
	require.NoError(t, database.Close())
	store, err = NewPostgresStore(database)
	require.ErrorContains(t, err, "initialize")
	require.Nil(t, store)
}

func TestPostgresStoreRepeatedSaveUsesExactDatabaseVersion(t *testing.T) {
	database := openTestDB(t)
	store := newTestStore(t, database)
	ctx := context.Background()
	sess := NewSession("/version-regression")
	require.NoError(t, store.Create(ctx, sess))
	persisted, err := store.Load(ctx, sess.ID)
	require.NoError(t, err)
	require.True(t, sess.CreatedAt.Equal(persisted.CreatedAt))
	require.True(t, sess.UpdatedAt.Equal(persisted.UpdatedAt))
	for _, title := range []string{"first", "second", "third"} {
		sess.Title = title
		require.NoError(t, store.Save(ctx, sess))
		persisted, err = store.Load(ctx, sess.ID)
		require.NoError(t, err)
		require.Equal(t, title, persisted.Title)
		require.True(t, sess.UpdatedAt.Equal(persisted.UpdatedAt), "client version must equal stored version")
	}
}

func TestPostgresStoreStaleWriterCannotOverwriteLatest(t *testing.T) {
	store := newTestStore(t, openTestDB(t))
	ctx := context.Background()
	sess := NewSession("/conflict-regression")
	require.NoError(t, store.Create(ctx, sess))
	stale, err := store.Load(ctx, sess.ID)
	require.NoError(t, err)
	sess.Title = "accepted"
	require.NoError(t, store.Save(ctx, sess))
	stale.Title = "must not overwrite"
	require.ErrorIs(t, store.Save(ctx, stale), ErrConflict)
	current, err := store.Load(ctx, sess.ID)
	require.NoError(t, err)
	require.Equal(t, "accepted", current.Title)
	require.NoError(t, store.Delete(ctx, sess.ID))
	require.ErrorIs(t, store.Save(ctx, sess), ErrNotFound)
}

func TestPostgresStoreCloseDoesNotCloseBorrowedPool(t *testing.T) {
	database := openTestDB(t)
	store := newTestStore(t, database)
	require.NoError(t, store.Close())
	require.NoError(t, database.PingContext(context.Background()))
}

func TestPostgresDerivedSessionsCanBeSavedImmediately(t *testing.T) {
	store := newTestStore(t, openTestDB(t))
	ctx := context.Background()
	parent := seedParent(t, store, 2)
	branch, err := store.Branch(ctx, parent.ID, 2)
	require.NoError(t, err)
	branch.Title = "saved branch"
	require.NoError(t, store.Save(ctx, branch))
	clone, err := store.Clone(ctx, parent.ID)
	require.NoError(t, err)
	clone.Title = "saved clone"
	require.NoError(t, store.Save(ctx, clone))
	branch.Messages = append(branch.Messages, branch.Messages[0])
	require.NoError(t, store.Save(ctx, branch))
	merged, err := store.Merge(ctx, branch.ID)
	require.NoError(t, err)
	merged.Title = "saved merge"
	require.NoError(t, store.Save(ctx, merged))
}

func TestPostgresConcurrentBranchesKeepEveryChild(t *testing.T) {
	store := newTestStore(t, openTestDB(t))
	ctx := context.Background()
	parent := seedParent(t, store, 2)
	const count = 12
	errors := make(chan error, count)
	var workers sync.WaitGroup
	for range count {
		workers.Go(func() {
			_, err := store.Branch(ctx, parent.ID, 2)
			errors <- err
		})
	}
	workers.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	current, err := store.Load(ctx, parent.ID)
	require.NoError(t, err)
	require.Len(t, current.Children, count)
	seen := make(map[string]bool)
	for _, id := range current.Children {
		require.False(t, seen[id])
		seen[id] = true
		child, err := store.Load(ctx, id)
		require.NoError(t, err)
		require.Equal(t, parent.ID, child.ParentID)
	}
}

func TestPostgresMigrationFailureRollsBackPartialSchema(t *testing.T) {
	database := openTestDB(t)
	ctx := context.Background()
	// An incompatible existing relation must fail startup and remain intact.
	_, err := database.ExecContext(ctx, `CREATE VIEW sessions AS SELECT 'preserved'::text AS id`)
	require.NoError(t, err)
	store, err := NewPostgresStore(database)
	require.ErrorContains(t, err, "initialize")
	require.Nil(t, store)
	var value string
	require.NoError(t, database.QueryRowContext(ctx, `SELECT id FROM sessions`).Scan(&value))
	require.Equal(t, "preserved", value)
	var routerExists bool
	require.NoError(t, database.QueryRowContext(ctx, `SELECT to_regclass('session_router') IS NOT NULL`).Scan(&routerExists))
	require.False(t, routerExists, "failed initialization must not leave partial tables")
}

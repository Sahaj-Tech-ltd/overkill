package session

import (
	"context"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRetentionCountsAllRootsDespiteChildCrowding(t *testing.T) {
	for _, mode := range []string{"postgres_atomic", "generic_store"} {
		t.Run(mode, func(t *testing.T) {
			db := openTestDB(t)
			store := newTestStore(t, db)
			base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
			var roots []*Session
			for i := range 12 {
				root := NewSession("/retention")
				root.Title = fmt.Sprintf("root-%02d", i)
				require.NoError(t, store.Create(t.Context(), root))
				roots = append(roots, root)
				_, err := db.ExecContext(t.Context(), `UPDATE sessions SET updated_at=$2 WHERE id=$1`, root.ID, base.Add(time.Duration(i)*time.Hour))
				require.NoError(t, err)
			}
			var children []*Session
			for i := range 80 {
				child := NewSession("/retention")
				child.ParentID = roots[len(roots)-1].ID
				require.NoError(t, store.Create(t.Context(), child))
				children = append(children, child)
				_, err := db.ExecContext(t.Context(), `UPDATE sessions SET updated_at=$2 WHERE id=$1`, child.ID, base.Add(-time.Duration(i+1)*time.Hour))
				require.NoError(t, err)
			}
			var runner Store = store
			if mode == "generic_store" {
				runner = struct{ Store }{store}
			}
			deleted, err := EnforceMax(t.Context(), runner, 2)
			require.NoError(t, err)
			require.Equal(t, 10, deleted)
			for i, root := range roots {
				loaded, err := store.Load(t.Context(), root.ID)
				if i < 10 {
					require.ErrorIs(t, err, ErrNotFound)
				} else {
					require.NoError(t, err)
					require.Equal(t, root.Title, loaded.Title)
				}
			}
			for _, child := range children {
				loaded, err := store.Load(t.Context(), child.ID)
				require.NoError(t, err)
				require.Equal(t, roots[11].ID, loaded.ParentID)
			}
		})
	}

}

func TestRetentionDeterministicTiesPreserveInitiallyNestedFamily(t *testing.T) {
	for _, generic := range []bool{false, true} {
		t.Run(fmt.Sprint(generic), func(t *testing.T) {
			db := openTestDB(t)
			store := newTestStore(t, db)
			same := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
			for _, id := range []string{"a", "b", "c"} {
				item := NewSession("/ties")
				item.ID = id
				require.NoError(t, store.Create(t.Context(), item))
				_, err := db.ExecContext(t.Context(), `UPDATE sessions SET updated_at=$2 WHERE id=$1`, id, same)
				require.NoError(t, err)
			}
			child := NewSession("/ties")
			child.ParentID = "a"
			require.NoError(t, store.Create(t.Context(), child))
			require.NoError(t, store.Bind(t.Context(), "fixture", "retention", "", "a"))
			var runner Store = store
			if generic {
				runner = struct{ Store }{store}
			}
			deleted, err := EnforceMax(t.Context(), runner, 1)
			require.NoError(t, err)
			require.Equal(t, 2, deleted)
			_, err = store.Load(t.Context(), "a")
			require.ErrorIs(t, err, ErrNotFound)
			_, err = store.Load(t.Context(), "b")
			require.ErrorIs(t, err, ErrNotFound)
			_, err = store.Load(t.Context(), "c")
			require.NoError(t, err)
			retained, err := store.Load(t.Context(), child.ID)
			require.NoError(t, err)
			require.Empty(t, retained.ParentID)
			bound, err := store.Resolve(t.Context(), "fixture", "retention", "", "")
			require.NoError(t, err)
			require.Empty(t, bound)
		})
	}
}
func TestRetentionAtomicPostgresFailureRollsBackFamiliesAndBindings(t *testing.T) {
	db := openTestDB(t)
	store := newTestStore(t, db)
	var roots []*Session
	for i := range 5 {
		root := NewSession("/rollback")
		root.Title = fmt.Sprint(i)
		require.NoError(t, store.Create(t.Context(), root))
		roots = append(roots, root)
	}
	child := NewSession("/rollback")
	child.ParentID = roots[0].ID
	require.NoError(t, store.Create(t.Context(), child))
	require.NoError(t, store.Bind(t.Context(), "fixture", "rollback", "", roots[0].ID))
	_, err := db.ExecContext(t.Context(), `CREATE FUNCTION reject_retention_delete() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture blocks delete'; END $$;CREATE TRIGGER reject_retention_delete BEFORE DELETE ON sessions FOR EACH ROW EXECUTE FUNCTION reject_retention_delete()`)
	require.NoError(t, err)
	deleted, err := EnforceMax(t.Context(), store, 1)
	require.Error(t, err)
	require.Zero(t, deleted)
	for _, root := range roots {
		_, err := store.Load(t.Context(), root.ID)
		require.NoError(t, err)
	}
	retained, err := store.Load(t.Context(), child.ID)
	require.NoError(t, err)
	require.Equal(t, roots[0].ID, retained.ParentID)
	route, err := store.Resolve(t.Context(), "fixture", "rollback", "", "")
	require.NoError(t, err)
	require.Equal(t, roots[0].ID, route)
	_, err = db.ExecContext(t.Context(), `DROP TRIGGER reject_retention_delete ON sessions`)
	require.NoError(t, err)
	deleted, err = EnforceMax(t.Context(), store, 1)
	require.NoError(t, err)
	require.Equal(t, 4, deleted)
}

type retentionListOverride struct {
	Store
	list func(ListOptions) ([]*Session, error)
}

func (s retentionListOverride) List(ctx context.Context, opts ListOptions) ([]*Session, error) {
	return s.list(opts)
}
func TestRetentionGenericRejectsRepeatedPagesBeforeDeleting(t *testing.T) {
	db := openTestDB(t)
	store := newTestStore(t, db)
	item := NewSession("/bad-pages")
	require.NoError(t, store.Create(t.Context(), item))
	calls := 0
	fake := retentionListOverride{Store: store, list: func(opts ListOptions) ([]*Session, error) {
		calls++
		page := make([]*Session, 64)
		for i := range page {
			page[i] = &Session{ID: fmt.Sprintf("id-%03d", i), UpdatedAt: item.UpdatedAt}
		}
		return page, nil
	}}
	deleted, err := EnforceMax(t.Context(), fake, 1)
	require.ErrorContains(t, err, "nonmonotonic")
	require.Zero(t, deleted)
	require.Equal(t, 2, calls)
	_, err = store.Load(t.Context(), item.ID)
	require.NoError(t, err)
}
func TestRetentionCancellationAndHugeCapDoNotDelete(t *testing.T) {
	for _, generic := range []bool{false, true} {
		t.Run(fmt.Sprint(generic), func(t *testing.T) {
			db := openTestDB(t)
			store := newTestStore(t, db)
			var runner Store = store
			if generic {
				runner = struct{ Store }{store}
			}
			item := NewSession("/cancel-retention")
			require.NoError(t, store.Create(t.Context(), item))
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			deleted, err := EnforceMax(ctx, runner, 1)
			require.Error(t, err)
			require.Zero(t, deleted)
			deleted, err = EnforceMax(t.Context(), runner, math.MaxInt)
			require.NoError(t, err)
			require.Zero(t, deleted)
			_, err = store.Load(t.Context(), item.ID)
			require.NoError(t, err)
		})
	}
}

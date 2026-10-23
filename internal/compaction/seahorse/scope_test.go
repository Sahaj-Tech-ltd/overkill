package seahorse

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/testpg"
)

func TestExecutionScopePersistsAndReloadsWithoutBaseSessionLeakage(t *testing.T) {
	ctx := context.Background()
	db, _ := testpg.Open(t)
	store, err := NewPostgresStore(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	scoped, err := WithHistoryScope(ctx, strings.Repeat("a", 32))
	if err != nil {
		t.Fatal(err)
	}
	other, err := WithHistoryScope(ctx, strings.Repeat("b", 32))
	if err != nil {
		t.Fatal(err)
	}
	fixture := newProviderFixture(t)
	history := fixtureHistory()
	c := testCompactor(t, store, "scope-parent", fixture, fixtureOptions())
	output, _, err := c.Compact(scoped, history, "parent-fixture", 4096)
	if err != nil {
		t.Fatal(err)
	}
	namespace := ScopedSession(scoped, "scope-parent")
	head, err := store.Load(ctx, namespace)
	if err != nil || head.HeadID == "" {
		t.Fatalf("missing scoped durable head: %#v %v", head, err)
	}
	for _, session := range []string{"scope-parent", ScopedSession(other, "scope-parent"), ScopedSession(scoped, "another-parent")} {
		state, err := store.Load(ctx, session)
		if err != nil || state.HeadID != "" {
			t.Fatalf("scoped data leaked to %s: %#v %v", session, state, err)
		}
	}
	calls := fixture.count()
	reloaded := testCompactor(t, store, "scope-parent", fixture, fixtureOptions())
	replay, _, err := reloaded.Compact(scoped, history, "parent-fixture", 4096)
	if err != nil || !reflect.DeepEqual(output, replay) || calls != fixture.count() {
		t.Fatalf("scope restart did not reuse exact committed DAG: %v", err)
	}
}

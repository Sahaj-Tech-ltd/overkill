package coordination

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func contractSpec(p Peer, name string, paths ...string) ContractSpec {
	return ContractSpec{Name: name, Endpoint: "GET /" + name, SchemaVersion: "v1", Schema: json.RawMessage(`{"type":"object","required":["id"],"properties":{"id":{"type":"string"}}}`), AcceptanceCriteria: []string{"response conforms to published schema"}, Artifacts: []ContractArtifact{{Name: "published schema", Path: name + ".json"}}, Workspace: p.Workspace, OwnedPaths: paths}
}

func TestContractListingBoundsCombinedSchemasWithoutLosingIndividualReads(t *testing.T) {
	store, front, back, _ := fixture(t)
	var last Contract
	for i := 0; i < 4; i++ {
		spec := contractSpec(front, fmt.Sprintf("large-%d", i))
		spec.Schema = json.RawMessage(`{"description":"` + strings.Repeat("x", 64*1024) + `"}`)
		spec.AcceptanceCriteria = make([]string, 64)
		for j := range spec.AcceptanceCriteria {
			spec.AcceptanceCriteria[j] = strings.Repeat("a", 4000)
		}
		var err error
		last, err = store.CreateContract(t.Context(), front.ID, spec)
		require.NoError(t, err)
	}
	all, err := store.ListContracts(t.Context(), back.ID)
	require.ErrorContains(t, err, "exceeds 1 MiB")
	require.Nil(t, all, "never return an apparently complete partial inventory")
	got, err := store.ReadContract(t.Context(), back.ID, last.ID)
	require.NoError(t, err)
	require.Equal(t, last.ID, got.ID)
}
func TestContractCRUDDurabilityAuthenticationAndRevisions(t *testing.T) {
	store, front, back, _ := fixture(t)
	c, err := store.CreateContract(t.Context(), front.ID, contractSpec(front, "users", "src/users"))
	require.NoError(t, err)
	require.Equal(t, int64(1), c.Revision)
	require.Equal(t, front.ID, c.OwnerPeerID)
	restarted, err := NewStore(t.Context(), store.db)
	require.NoError(t, err)
	got, err := restarted.ReadContract(t.Context(), back.ID, c.ID)
	require.NoError(t, err)
	require.Equal(t, c, got)
	all, err := restarted.ListContracts(t.Context(), back.ID)
	require.NoError(t, err)
	require.Len(t, all, 1)
	outsider, _, err := store.RegisterPeer(t.Context(), Peer{ProjectID: "elsewhere", Name: "outsider", Role: "other project", Workspace: t.TempDir()})
	require.NoError(t, err)
	_, err = store.ReadContract(t.Context(), outsider.ID, c.ID)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = store.AcknowledgeContract(t.Context(), outsider.ID, c.ID, 1)
	require.ErrorIs(t, err, ErrNotFound)
	_, err = store.UpdateContract(t.Context(), back.ID, c.ID, 1, c.Spec)
	require.ErrorIs(t, err, ErrUnauthorized)
	require.ErrorIs(t, store.DeleteContract(t.Context(), back.ID, c.ID, 1), ErrUnauthorized)
	_, err = store.UpdateContract(t.Context(), front.ID, c.ID, 8, c.Spec)
	require.ErrorIs(t, err, ErrConflict)
	ack, err := store.AcknowledgeContract(t.Context(), back.ID, c.ID, 1)
	require.NoError(t, err)
	duplicate, err := store.AcknowledgeContract(t.Context(), back.ID, c.ID, 1)
	require.NoError(t, err)
	require.Equal(t, ack, duplicate)
	ready, err := store.ContractReadiness(t.Context(), back.ID, c.ID)
	require.NoError(t, err)
	require.True(t, ready.Ready)
	require.Contains(t, ready.Source, "task execution/acceptance unavailable")
	spec := c.Spec
	spec.Schema = json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer"}}}`)
	_, err = store.UpdateContract(t.Context(), front.ID, c.ID, 1, spec)
	require.ErrorIs(t, err, ErrContractValidation)
	spec.SchemaVersion = "v2"
	_, err = store.UpdateContract(t.Context(), front.ID, c.ID, 1, spec)
	require.ErrorIs(t, err, ErrContractValidation)
	spec.CompatibilityChanges = []CompatibilityChange{{Kind: "breaking", FromVersion: "v1", ToVersion: "v2", Description: "id is now integer"}}
	changed, err := store.UpdateContract(t.Context(), front.ID, c.ID, 1, spec)
	require.NoError(t, err)
	require.Equal(t, int64(2), changed.Revision)
	old, err := store.ContractRevision(t.Context(), back.ID, c.ID, 1)
	require.NoError(t, err)
	require.JSONEq(t, string(c.Spec.Schema), string(old.Schema))
	ready, err = store.ContractReadiness(t.Context(), back.ID, c.ID)
	require.NoError(t, err)
	require.False(t, ready.Ready)
	_, err = store.AcknowledgeContract(t.Context(), back.ID, c.ID, 1)
	require.ErrorIs(t, err, ErrConflict)
	_, err = store.AcknowledgeContract(t.Context(), back.ID, c.ID, 2)
	require.NoError(t, err)
	ready, err = store.ContractReadiness(t.Context(), back.ID, c.ID)
	require.NoError(t, err)
	require.True(t, ready.Ready)
	require.ErrorIs(t, store.DeleteContract(t.Context(), front.ID, c.ID, 1), ErrConflict)
	require.NoError(t, store.DisablePeer(t.Context(), back.ID))
	_, err = store.ReadContract(t.Context(), back.ID, c.ID)
	require.ErrorIs(t, err, ErrUnauthorized)
	_, err = store.AcknowledgeContract(t.Context(), back.ID, c.ID, 2)
	require.ErrorIs(t, err, ErrUnauthorized)
	require.NoError(t, store.DeleteContract(t.Context(), front.ID, c.ID, 2))
	_, err = store.ReadContract(t.Context(), front.ID, c.ID)
	require.ErrorIs(t, err, ErrNotFound)
	replacement, err := store.CreateContract(t.Context(), front.ID, contractSpec(front, "replacement", "src/users"))
	require.NoError(t, err)
	require.NotEqual(t, c.ID, replacement.ID)
}
func TestContractDependenciesReadinessAndCycles(t *testing.T) {
	store, front, back, _ := fixture(t)
	upstream, err := store.CreateContract(t.Context(), back.ID, contractSpec(back, "upstream", "api"))
	require.NoError(t, err)
	downstreamSpec := contractSpec(front, "consumer", "ui")
	downstreamSpec.Dependencies = []ContractDependency{{ContractID: upstream.ID, Revision: 1}}
	downstream, err := store.CreateContract(t.Context(), front.ID, downstreamSpec)
	require.NoError(t, err)
	_, err = store.AcknowledgeContract(t.Context(), back.ID, downstream.ID, 1)
	require.NoError(t, err)
	ready, err := store.ContractReadiness(t.Context(), back.ID, downstream.ID)
	require.NoError(t, err)
	require.False(t, ready.Ready)
	_, err = store.AcknowledgeContract(t.Context(), front.ID, upstream.ID, 1)
	require.NoError(t, err)
	ready, err = store.ContractReadiness(t.Context(), back.ID, downstream.ID)
	require.NoError(t, err)
	require.True(t, ready.Ready)
	cycle := upstream.Spec
	cycle.Dependencies = []ContractDependency{{ContractID: downstream.ID, Revision: 1}}
	_, err = store.UpdateContract(t.Context(), back.ID, upstream.ID, 1, cycle)
	require.ErrorIs(t, err, ErrDependencyCycle)
	require.ErrorIs(t, store.DeleteContract(t.Context(), back.ID, upstream.ID, 1), ErrConflict)
	revised := upstream.Spec
	revised.Name = "upstream revised criteria"
	upstream, err = store.UpdateContract(t.Context(), back.ID, upstream.ID, 1, revised)
	require.NoError(t, err)
	ready, err = store.ContractReadiness(t.Context(), back.ID, downstream.ID)
	require.NoError(t, err)
	require.False(t, ready.Ready)
	require.Contains(t, ready.Blockers, ContractBlocker{ContractID: upstream.ID, Reason: "dependency_revision_changed", ExpectedRevision: 1, ActualRevision: 2})
	_, err = store.AcknowledgeContract(t.Context(), front.ID, upstream.ID, 2)
	require.NoError(t, err)
	ready, err = store.ContractReadiness(t.Context(), back.ID, downstream.ID)
	require.NoError(t, err)
	require.False(t, ready.Ready, "new upstream ack cannot silently repin dependent spec")
	downstream.Spec.Dependencies[0].Revision = 2
	downstream, err = store.UpdateContract(t.Context(), front.ID, downstream.ID, 1, downstream.Spec)
	require.NoError(t, err)
	ready, err = store.ContractReadiness(t.Context(), back.ID, downstream.ID)
	require.NoError(t, err)
	require.False(t, ready.Ready, "consumer must acknowledge changed dependent contract revision")
	_, err = store.AcknowledgeContract(t.Context(), back.ID, downstream.ID, 2)
	require.NoError(t, err)
	ready, err = store.ContractReadiness(t.Context(), back.ID, downstream.ID)
	require.NoError(t, err)
	require.True(t, ready.Ready)
	require.NoError(t, store.DisablePeer(t.Context(), front.ID))
	ready, err = store.ContractReadiness(t.Context(), back.ID, downstream.ID)
	require.NoError(t, err)
	require.False(t, ready.Ready)
	require.Contains(t, ready.Blockers, ContractBlocker{ContractID: downstream.ID, Reason: "owner_disabled"})
}
func TestContractOwnershipConcurrentAndPortableAliases(t *testing.T) {
	store, front, back, _ := fixture(t)
	other, err := NewStore(t.Context(), store.db)
	require.NoError(t, err)
	var wg sync.WaitGroup
	results := make(chan error, 2)
	start := make(chan struct{})
	for i, p := range []Peer{front, back} {
		wg.Go(func() {
			<-start
			path := "SRC/Caf\u00e9"
			if i == 1 {
				path = "src/Cafe\u0301/child"
			}
			_, err := []*Store{store, other}[i].CreateContract(t.Context(), p.ID, contractSpec(p, p.Name, path))
			results <- err
		})
	}
	close(start)
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else {
			require.ErrorIs(t, err, ErrOwnershipConflict)
			conflict++
		}
	}
	require.Equal(t, 1, success)
	require.Equal(t, 1, conflict)
	key, err := NormalizeOwnedPath("SRC/Caf\u00e9")
	require.NoError(t, err)
	alias, err := NormalizeOwnedPath("src/Cafe\u0301")
	require.NoError(t, err)
	require.Equal(t, key, alias)
	// Prefix boundaries do not mistake api-v2 for a child of api.
	_, err = store.CreateContract(t.Context(), front.ID, contractSpec(front, "api", "api"))
	require.NoError(t, err)
	_, err = store.CreateContract(t.Context(), back.ID, contractSpec(back, "api-v2", "api-v2"))
	require.NoError(t, err)
	_, err = store.CreateContract(t.Context(), back.ID, contractSpec(back, "ancestor", "."))
	require.ErrorIs(t, err, ErrOwnershipConflict)
}
func TestContractOptimisticConcurrentUpdatesAndRollback(t *testing.T) {
	store, front, back, _ := fixture(t)
	c, err := store.CreateContract(t.Context(), front.ID, contractSpec(front, "original", "owned"))
	require.NoError(t, err)
	var wg sync.WaitGroup
	results := make(chan error, 12)
	for i := range 12 {
		wg.Go(func() {
			spec := c.Spec
			spec.Name = strings.Repeat("x", i+1)
			_, err := store.UpdateContract(t.Context(), front.ID, c.ID, 1, spec)
			results <- err
		})
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else {
			require.ErrorIs(t, err, ErrConflict)
		}
	}
	require.Equal(t, 1, success)
	current, err := store.ReadContract(t.Context(), front.ID, c.ID)
	require.NoError(t, err)
	require.Equal(t, int64(2), current.Revision)
	blocked := current.Spec
	blocked.OwnedPaths = []string{"new", "owned"}
	blocked.Dependencies = []ContractDependency{{ContractID: c.ID, Revision: 2}}
	_, err = store.UpdateContract(t.Context(), front.ID, c.ID, 2, blocked)
	require.ErrorIs(t, err, ErrDependencyCycle)
	_, err = store.CreateContract(t.Context(), back.ID, contractSpec(back, "rollback-proof", "new"))
	require.NoError(t, err, "failed update must not persist ownership paths")
}
func TestContractValidationAndSymlinkOwnership(t *testing.T) {
	store, front, back, _ := fixture(t)
	for _, p := range []string{"../escape", "/absolute", "C:/drive", "dir\\file", "dir/*", ""} {
		spec := contractSpec(front, "invalid", p)
		_, err := store.CreateContract(t.Context(), front.ID, spec)
		require.ErrorIs(t, err, ErrContractValidation, p)
	}
	spec := contractSpec(front, "duplicates", "Foo", "foo")
	_, err := store.CreateContract(t.Context(), front.ID, spec)
	require.ErrorIs(t, err, ErrContractValidation)
	spec = contractSpec(front, "foreign-workspace", "valid")
	spec.Workspace = back.Workspace
	_, err = store.CreateContract(t.Context(), front.ID, spec)
	require.ErrorIs(t, err, ErrContractValidation)
	spec = contractSpec(front, "bad-schema", "valid")
	spec.Schema = json.RawMessage(`null`)
	_, err = store.CreateContract(t.Context(), front.ID, spec)
	require.ErrorIs(t, err, ErrContractValidation)
	spec = contractSpec(front, "bad-criteria", "valid")
	spec.AcceptanceCriteria = nil
	_, err = store.CreateContract(t.Context(), front.ID, spec)
	require.ErrorIs(t, err, ErrContractValidation)
	require.NoError(t, os.MkdirAll(filepath.Join(front.Workspace, "real"), 0700))
	err = os.Symlink(filepath.Join(front.Workspace, "real"), filepath.Join(front.Workspace, "alias"))
	if err != nil {
		t.Skipf("host does not support symlink creation: %v", err)
	}
	_, err = store.CreateContract(t.Context(), front.ID, contractSpec(front, "symlink", "alias/file"))
	require.ErrorIs(t, err, ErrContractValidation)
	_, err = store.CreateContract(t.Context(), front.ID, contractSpec(front, "real", "real"))
	require.NoError(t, err)
	require.ErrorIs(t, store.CheckOwnedPaths(t.Context(), front.ID, []string{"alias/file"}), ErrContractValidation)
}
func TestContractGuardsHoldLockThroughSideEffect(t *testing.T) {
	store, front, _, _ := fixture(t)
	back, _, err := store.RegisterPeer(t.Context(), Peer{ProjectID: front.ProjectID, Name: "shared-backend", Role: "shared workspace", Workspace: front.Workspace})
	require.NoError(t, err)
	started, release := make(chan struct{}), make(chan struct{})
	guardDone := make(chan error, 1)
	go func() {
		guardDone <- store.WithOwnedPaths(t.Context(), front.ID, []string{"before-claim.txt"}, func(context.Context) error { close(started); <-release; return nil })
	}()
	<-started
	claimDone := make(chan error, 1)
	go func() {
		_, err := store.CreateContract(t.Context(), back.ID, contractSpec(back, "new-owner", "before-claim.txt"))
		claimDone <- err
	}()
	select {
	case err := <-claimDone:
		t.Fatalf("claim mutated while callback held project lock: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	require.NoError(t, <-guardDone)
	require.NoError(t, <-claimDone)
	called := false
	err = store.WithOwnedPaths(t.Context(), front.ID, []string{"before-claim.txt"}, func(context.Context) error { called = true; return nil })
	require.ErrorIs(t, err, ErrOwnershipConflict)
	require.False(t, called)
	require.ErrorIs(t, store.WithUnrestrictedWorkspace(t.Context(), front.ID, func(context.Context) error { called = true; return nil }), ErrOwnershipConflict)
	require.False(t, called)
	require.NoError(t, store.WithOwnedPaths(t.Context(), back.ID, []string{"BEFORE-CLAIM.TXT"}, func(context.Context) error { return nil }))
	require.ErrorIs(t, store.CheckOwnedPaths(t.Context(), back.ID, []string{"unclaimed.txt"}), ErrOwnershipConflict)
	sentinel := errors.New("tool failed")
	require.ErrorIs(t, store.WithUnrestrictedWorkspace(t.Context(), back.ID, func(context.Context) error { return sentinel }), sentinel)
	// Failed callback must release the transaction/advisory lock.
	require.NoError(t, store.WithUnrestrictedWorkspace(t.Context(), back.ID, func(context.Context) error { return nil }))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, store.WithUnrestrictedWorkspace(ctx, back.ID, func(context.Context) error { return nil }), context.Canceled)
}

func TestUnrestrictedWorkspacePhysicalIsolationAndDisabledReservations(t *testing.T) {
	for _, relationship := range []string{"same", "nested-owner", "nested-caller", "sibling", "prefix-sibling", "symlink-alias"} {
		t.Run(relationship, func(t *testing.T) {
			store, _, _, _ := fixture(t)
			root := t.TempDir()
			callerRoot, ownerRoot := filepath.Join(root, "work"), filepath.Join(root, "isolated")
			switch relationship {
			case "same", "symlink-alias":
				ownerRoot = callerRoot
			case "nested-owner":
				ownerRoot = filepath.Join(callerRoot, "child")
			case "nested-caller":
				ownerRoot, callerRoot = callerRoot, filepath.Join(callerRoot, "child")
			case "prefix-sibling":
				ownerRoot = callerRoot + "-other"
			}
			require.NoError(t, os.MkdirAll(callerRoot, 0700))
			require.NoError(t, os.MkdirAll(ownerRoot, 0700))
			if relationship == "symlink-alias" {
				alias := filepath.Join(root, "alias")
				require.NoError(t, os.Symlink(ownerRoot, alias))
				ownerRoot = alias
			}
			caller, _, err := store.RegisterPeer(t.Context(), Peer{ProjectID: "isolated-guards", Name: "caller", Role: "caller", Workspace: callerRoot})
			require.NoError(t, err)
			owner, _, err := store.RegisterPeer(t.Context(), Peer{ProjectID: caller.ProjectID, Name: "owner", Role: "owner", Workspace: ownerRoot})
			require.NoError(t, err)
			_, err = store.CreateContract(t.Context(), owner.ID, contractSpec(owner, "reservation", "reserved"))
			require.NoError(t, err)
			for _, disabled := range []bool{false, true} {
				if disabled {
					require.NoError(t, store.DisablePeer(t.Context(), owner.ID))
				}
				called := false
				err = store.WithUnrestrictedWorkspace(t.Context(), caller.ID, func(context.Context) error { called = true; return nil })
				if relationship == "sibling" || relationship == "prefix-sibling" {
					require.NoError(t, err)
					require.True(t, called)
				} else {
					require.ErrorIs(t, err, ErrOwnershipConflict)
					require.False(t, called)
				}
			}
		})
	}
}

func TestUnrestrictedWorkspaceRejectsChangedCanonicalRoot(t *testing.T) {
	store, front, back, _ := fixture(t)
	_, err := store.CreateContract(t.Context(), back.ID, contractSpec(back, "reserved", "reserved"))
	require.NoError(t, err)
	// Replacing an owner's registered directory with an alias must fail closed,
	// even if the alias currently points to another disjoint directory.
	require.NoError(t, os.Remove(back.Workspace))
	require.NoError(t, os.Symlink(t.TempDir(), back.Workspace))
	called := false
	require.ErrorIs(t, store.WithUnrestrictedWorkspace(t.Context(), front.ID, func(context.Context) error { called = true; return nil }), ErrContractValidation)
	require.False(t, called)
	// A path with the same spelling but no directory is not an isolated workspace.
	require.NoError(t, os.Remove(back.Workspace))
	require.NoError(t, os.WriteFile(back.Workspace, []byte("replaced"), 0600))
	require.ErrorIs(t, store.WithUnrestrictedWorkspace(t.Context(), front.ID, func(context.Context) error { called = true; return nil }), ErrContractValidation)
	require.False(t, called)
}

func TestWorkspaceRootOverlapPortableBoundaries(t *testing.T) {
	for _, pair := range [][2]string{{"/", "/work"}, {"/work", "/work/nested"}, {"/WORK/Caf\u00e9", "/work/Cafe\u0301/child"}} {
		require.True(t, workspaceRootsOverlap(pair[0], pair[1]))
		require.True(t, workspaceRootsOverlap(pair[1], pair[0]))
	}
	require.False(t, workspaceRootsOverlap("/work", "/work-other"))
}

func TestContractGuardsCancellationIsolationAndDisableFencing(t *testing.T) {
	store, front, back, _ := fixture(t)
	other, _, err := store.RegisterPeer(t.Context(), Peer{ProjectID: "other-project", Name: "other", Role: "separate project", Workspace: t.TempDir()})
	require.NoError(t, err)
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- store.WithUnrestrictedWorkspace(t.Context(), front.ID, func(context.Context) error { close(entered); <-release; return nil })
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("guard did not enter callback")
	}
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	// An unrelated project's lock and identical path namespace remain independent.
	otherCtx, cancelOther := context.WithTimeout(t.Context(), time.Second)
	defer cancelOther()
	_, err = store.CreateContract(otherCtx, other.ID, contractSpec(other, "independent", "same-path"))
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 80*time.Millisecond)
	defer cancel()
	_, err = store.CreateContract(ctx, back.ID, contractSpec(back, "waiter", "same-path"))
	require.Error(t, err)
	require.ErrorIs(t, ctx.Err(), context.DeadlineExceeded)
	disabled := make(chan error, 1)
	go func() { disabled <- store.DisablePeer(t.Context(), front.ID) }()
	select {
	case err := <-disabled:
		t.Fatalf("disable crossed live callback peer fence: %v", err)
	case <-time.After(80 * time.Millisecond):
	}
	close(release)
	released = true
	require.NoError(t, <-done)
	require.NoError(t, <-disabled)
	called := false
	require.ErrorIs(t, store.WithUnrestrictedWorkspace(t.Context(), front.ID, func(context.Context) error { called = true; return nil }), ErrUnauthorized)
	require.False(t, called)
	contracts, err := store.ListContracts(t.Context(), back.ID)
	require.NoError(t, err)
	require.Empty(t, contracts, "cancelled blocked claim must roll back")
}

func TestContractReadinessRecursesThroughPinnedDependencies(t *testing.T) {
	store, front, back, _ := fixture(t)
	leaf, err := store.CreateContract(t.Context(), front.ID, contractSpec(front, "leaf", "leaf"))
	require.NoError(t, err)
	middleSpec := contractSpec(back, "middle", "middle")
	middleSpec.Dependencies = []ContractDependency{{ContractID: leaf.ID, Revision: 1}}
	middle, err := store.CreateContract(t.Context(), back.ID, middleSpec)
	require.NoError(t, err)
	topSpec := contractSpec(front, "top", "top")
	topSpec.Dependencies = []ContractDependency{{ContractID: middle.ID, Revision: 1}}
	top, err := store.CreateContract(t.Context(), front.ID, topSpec)
	require.NoError(t, err)
	for _, ack := range []struct{ peer, id string }{{back.ID, top.ID}, {front.ID, middle.ID}, {back.ID, leaf.ID}} {
		_, err = store.AcknowledgeContract(t.Context(), ack.peer, ack.id, 1)
		require.NoError(t, err)
	}
	ready, err := store.ContractReadiness(t.Context(), back.ID, top.ID)
	require.NoError(t, err)
	require.True(t, ready.Ready)
	leaf.Spec.AcceptanceCriteria = append(leaf.Spec.AcceptanceCriteria, "new criterion")
	_, err = store.UpdateContract(t.Context(), front.ID, leaf.ID, 1, leaf.Spec)
	require.NoError(t, err)
	ready, err = store.ContractReadiness(t.Context(), back.ID, top.ID)
	require.NoError(t, err)
	require.False(t, ready.Ready)
	require.Contains(t, ready.Blockers, ContractBlocker{ContractID: leaf.ID, Reason: "dependency_revision_changed", ExpectedRevision: 1, ActualRevision: 2})
}

func TestContractSchemaComparisonPreservesLargeNumbers(t *testing.T) {
	require.False(t, jsonSemanticEqual(json.RawMessage(`{"maximum":9007199254740992}`), json.RawMessage(`{"maximum":9007199254740993}`)))
	require.True(t, jsonSemanticEqual(json.RawMessage(`{"type":"object","maximum":10}`), json.RawMessage(`{ "maximum":10,"type":"object"}`)))
}

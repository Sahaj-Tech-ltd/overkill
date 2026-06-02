package session

import (
	"context"
	"errors"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

// seedParent creates a parent session with `n` messages and persists it.
func seedParent(t *testing.T, store *PostgresStore, n int) *Session {
	t.Helper()
	p := NewSession("/repo")
	p.Title = "trunk"
	p.Model = "openai/gpt-4o"
	for i := 0; i < n; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		p.Messages = append(p.Messages, providers.Message{Role: role, Content: "msg"})
	}
	p.TurnCount = n
	if err := store.Create(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestClone_MissingParent(t *testing.T) {
	store := NewPostgresStore(openTestDB(t))
	defer store.Close()
	if _, err := store.Clone(context.Background(), "nope"); err == nil {
		t.Error("missing parent should error")
	}
}

func TestClone_EmptyParentID(t *testing.T) {
	store := NewPostgresStore(openTestDB(t))
	defer store.Close()
	if _, err := store.Clone(context.Background(), ""); err == nil {
		t.Error("empty parentID should error")
	}
}

func TestClone_CopiesAllMessages(t *testing.T) {
	store := NewPostgresStore(openTestDB(t))
	defer store.Close()
	parent := seedParent(t, store, 4)

	child, err := store.Clone(context.Background(), parent.ID)
	if err != nil {
		t.Fatalf("clone: %v", err)
	}
	if child.ID == parent.ID {
		t.Error("clone must have fresh ID")
	}
	if len(child.Messages) != 4 {
		t.Errorf("clone should copy all 4 messages, got %d", len(child.Messages))
	}
	if child.ParentID != parent.ID {
		t.Errorf("clone parent link: %s", child.ParentID)
	}
	if child.BranchedAtTurn != 4 {
		t.Errorf("clone BranchedAtTurn should equal message count, got %d", child.BranchedAtTurn)
	}
}

func TestClone_LinksIntoParentChildren(t *testing.T) {
	store := NewPostgresStore(openTestDB(t))
	defer store.Close()
	parent := seedParent(t, store, 2)
	child, _ := store.Clone(context.Background(), parent.ID)

	reloaded, err := store.Load(context.Background(), parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Children) != 1 || reloaded.Children[0] != child.ID {
		t.Errorf("parent Children not updated: %+v", reloaded.Children)
	}
}

func TestClone_DivergesFromParent(t *testing.T) {
	store := NewPostgresStore(openTestDB(t))
	defer store.Close()
	parent := seedParent(t, store, 2)
	child, _ := store.Clone(context.Background(), parent.ID)

	// Mutate child — parent must stay untouched.
	child.Messages = append(child.Messages, providers.Message{Role: "user", Content: "child-only"})
	if err := store.Save(context.Background(), child); err != nil {
		t.Fatal(err)
	}
	reloaded, _ := store.Load(context.Background(), parent.ID)
	if len(reloaded.Messages) != 2 {
		t.Errorf("parent must not see child mutations: got %d messages", len(reloaded.Messages))
	}
}

func TestMerge_FastForwardsParent(t *testing.T) {
	store := NewPostgresStore(openTestDB(t))
	defer store.Close()
	parent := seedParent(t, store, 2)

	// Branch at 2 (end of parent), then have the child accumulate
	// new messages.
	child, err := store.Branch(context.Background(), parent.ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	child.Messages = append(child.Messages,
		providers.Message{Role: "user", Content: "new-from-child"},
		providers.Message{Role: "assistant", Content: "child-reply"},
	)
	if err := store.Save(context.Background(), child); err != nil {
		t.Fatal(err)
	}

	merged, err := store.Merge(context.Background(), child.ID)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if len(merged.Messages) != 4 {
		t.Errorf("parent should have 4 messages after merge, got %d", len(merged.Messages))
	}
	if merged.Messages[2].Content != "new-from-child" {
		t.Errorf("merged tail wrong: %+v", merged.Messages[2])
	}
	if merged.TurnCount != 4 {
		t.Errorf("TurnCount not updated: %d", merged.TurnCount)
	}
}

func TestMerge_RejectsDivergedParent(t *testing.T) {
	store := NewPostgresStore(openTestDB(t))
	defer store.Close()
	parent := seedParent(t, store, 2)

	child, _ := store.Branch(context.Background(), parent.ID, 2)
	child.Messages = append(child.Messages, providers.Message{Role: "user", Content: "child-tail"})
	_ = store.Save(context.Background(), child)

	// Parent continues — diverges.
	parent.Messages = append(parent.Messages, providers.Message{Role: "user", Content: "parent-tail"})
	_ = store.Save(context.Background(), parent)

	if _, err := store.Merge(context.Background(), child.ID); !errors.Is(err, ErrMergeDiverged) {
		t.Errorf("expected ErrMergeDiverged, got %v", err)
	}
}

func TestMerge_NoChangesIsNoOp(t *testing.T) {
	store := NewPostgresStore(openTestDB(t))
	defer store.Close()
	parent := seedParent(t, store, 3)
	child, _ := store.Branch(context.Background(), parent.ID, 3)

	// Child made no new messages.
	merged, err := store.Merge(context.Background(), child.ID)
	if err != nil {
		t.Fatalf("merge with no changes should not error, got %v", err)
	}
	if len(merged.Messages) != 3 {
		t.Errorf("merged length: %d", len(merged.Messages))
	}
}

func TestMerge_OrphanChildErrors(t *testing.T) {
	store := NewPostgresStore(openTestDB(t))
	defer store.Close()
	orphan := NewSession("/tmp") // no ParentID
	if err := store.Create(context.Background(), orphan); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Merge(context.Background(), orphan.ID); err == nil {
		t.Error("child without ParentID should error on merge")
	}
}

func TestMerge_MissingChildErrors(t *testing.T) {
	store := NewPostgresStore(openTestDB(t))
	defer store.Close()
	if _, err := store.Merge(context.Background(), "nope"); err == nil {
		t.Error("missing child should error")
	}
}

func TestMerge_EmptyChildIDErrors(t *testing.T) {
	store := NewPostgresStore(openTestDB(t))
	defer store.Close()
	if _, err := store.Merge(context.Background(), ""); err == nil {
		t.Error("empty childID should error")
	}
}

func TestMerge_ChildSurvivesUntouched(t *testing.T) {
	store := NewPostgresStore(openTestDB(t))
	defer store.Close()
	parent := seedParent(t, store, 2)
	child, _ := store.Branch(context.Background(), parent.ID, 2)
	child.Messages = append(child.Messages, providers.Message{Role: "user", Content: "kept"})
	_ = store.Save(context.Background(), child)

	if _, err := store.Merge(context.Background(), child.ID); err != nil {
		t.Fatal(err)
	}
	// Child must still be loadable with its original tail.
	loaded, err := store.Load(context.Background(), child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Messages) != 3 {
		t.Errorf("child should survive merge with its messages intact, got %d", len(loaded.Messages))
	}
}

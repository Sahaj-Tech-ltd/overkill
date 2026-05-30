package session

import (
	"context"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

func TestBranch_MissingParent(t *testing.T) {
	store := NewPostgresStore(openTestDB(t))
	defer store.Close()
	if _, err := store.Branch(context.Background(), "nope", 0); err == nil {
		t.Error("missing parent should error")
	}
}

func TestBranch_NegativeTurn(t *testing.T) {
	store := NewPostgresStore(openTestDB(t))
	defer store.Close()
	parent := NewSession("/tmp")
	if err := store.Create(context.Background(), parent); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Branch(context.Background(), parent.ID, -1); err == nil {
		t.Error("negative atTurn should error")
	}
}

func TestBranch_CopiesPrefixAndDiverges(t *testing.T) {
	store := NewPostgresStore(openTestDB(t))
	defer store.Close()

	parent := NewSession("/repo")
	parent.Title = "main work"
	parent.Model = "openai/gpt-4o"
	parent.Messages = []providers.Message{
		{Role: "user", Content: "do A"},
		{Role: "assistant", Content: "doing A"},
		{Role: "user", Content: "do B"},
		{Role: "assistant", Content: "doing B"},
	}
	if err := store.Create(context.Background(), parent); err != nil {
		t.Fatal(err)
	}

	child, err := store.Branch(context.Background(), parent.ID, 2)
	if err != nil {
		t.Fatalf("branch: %v", err)
	}
	if child.ParentID != parent.ID {
		t.Errorf("child.ParentID = %q, want %q", child.ParentID, parent.ID)
	}
	if child.BranchedAtTurn != 2 {
		t.Errorf("child.BranchedAtTurn = %d, want 2", child.BranchedAtTurn)
	}
	if len(child.Messages) != 2 {
		t.Errorf("child should have 2 prefix messages, got %d", len(child.Messages))
	}
	if child.Model != parent.Model {
		t.Errorf("child inherits model: got %q, want %q", child.Model, parent.Model)
	}

	// Reload parent — Children should contain the child.
	reloaded, err := store.Load(context.Background(), parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Children) != 1 || reloaded.Children[0] != child.ID {
		t.Errorf("parent.Children = %v, want [%s]", reloaded.Children, child.ID)
	}

	// Mutating the child's messages doesn't bleed into the parent.
	child.Messages = append(child.Messages, providers.Message{Role: "user", Content: "do C on the branch"})
	_ = store.Save(context.Background(), child)
	reloadedParent, _ := store.Load(context.Background(), parent.ID)
	if len(reloadedParent.Messages) != 4 {
		t.Errorf("parent messages should still be 4 after child grows, got %d", len(reloadedParent.Messages))
	}
}

func TestBranch_TurnBeyondHistoryClamps(t *testing.T) {
	store := NewPostgresStore(openTestDB(t))
	defer store.Close()
	parent := NewSession("/tmp")
	parent.Messages = []providers.Message{
		{Role: "user", Content: "x"},
		{Role: "assistant", Content: "y"},
	}
	if err := store.Create(context.Background(), parent); err != nil {
		t.Fatal(err)
	}
	child, err := store.Branch(context.Background(), parent.ID, 999)
	if err != nil {
		t.Fatal(err)
	}
	if len(child.Messages) != 2 {
		t.Errorf("beyond-history atTurn should clamp to len, got %d", len(child.Messages))
	}
	if child.BranchedAtTurn != 2 {
		t.Errorf("BranchedAtTurn should clamp, got %d", child.BranchedAtTurn)
	}
}

func TestBranch_ZeroTurnCopiesNothing(t *testing.T) {
	store := NewPostgresStore(openTestDB(t))
	defer store.Close()
	parent := NewSession("/tmp")
	parent.Messages = []providers.Message{{Role: "user", Content: "hi"}}
	_ = store.Create(context.Background(), parent)

	child, err := store.Branch(context.Background(), parent.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(child.Messages) != 0 {
		t.Errorf("atTurn=0 should copy nothing, got %d messages", len(child.Messages))
	}
}

func TestBranch_MultipleChildren(t *testing.T) {
	store := NewPostgresStore(openTestDB(t))
	defer store.Close()
	parent := NewSession("/tmp")
	parent.Messages = []providers.Message{
		{Role: "user", Content: "a"},
		{Role: "assistant", Content: "b"},
	}
	_ = store.Create(context.Background(), parent)

	c1, _ := store.Branch(context.Background(), parent.ID, 1)
	c2, _ := store.Branch(context.Background(), parent.ID, 2)

	reloaded, _ := store.Load(context.Background(), parent.ID)
	if len(reloaded.Children) != 2 {
		t.Fatalf("expected 2 children, got %v", reloaded.Children)
	}
	if reloaded.Children[0] != c1.ID || reloaded.Children[1] != c2.ID {
		t.Errorf("children order wrong: %v", reloaded.Children)
	}
}

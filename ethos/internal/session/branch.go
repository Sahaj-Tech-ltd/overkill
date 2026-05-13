// Package session — branching support for tree-structured sessions
// (Phase 1.5 #3). A branch is a new session whose ParentID points to
// an existing session and whose Messages are a PREFIX of the parent's
// up to and including a chosen turn index. After branching the parent
// and child diverge — edits to one don't affect the other.
package session

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

// Brancher is the optional store capability that supports tree-structured
// sessions. *BadgerStore satisfies this. Defined as a separate interface
// (not added to Store) so test doubles can opt in without implementing
// the full CRUD surface.
type Brancher interface {
	Branch(ctx context.Context, parentID string, atTurn int) (*Session, error)
}

// Branch creates a new session forked from parentID at message-index
// atTurn (inclusive). The child inherits the parent's model/provider/
// folder/title-prefix but starts with a fresh ID, no cost, and a clean
// CreatedAt. The parent gets the child's ID appended to its Children
// list and is persisted.
//
// atTurn semantics:
//   - 0           = branch from the very start (no messages copied)
//   - 1..len(M)-1 = branch after that many messages
//   - >= len(M)   = branch from the END of the parent (copy all)
//   - negative    = error
//
// Concurrent calls on the same parent are serialised by load → save.
// The store's Save is a full overwrite, so two concurrent Branch calls
// can race on the parent's Children list; callers needing strict
// safety should serialise externally for now.
func (s *BadgerStore) Branch(ctx context.Context, parentID string, atTurn int) (*Session, error) {
	if parentID == "" {
		return nil, fmt.Errorf("session: branch: parentID is required")
	}
	if atTurn < 0 {
		return nil, fmt.Errorf("session: branch: atTurn must be >= 0, got %d", atTurn)
	}
	parent, err := s.Load(ctx, parentID)
	if err != nil {
		return nil, fmt.Errorf("session: branch: load parent: %w", err)
	}
	if parent == nil {
		return nil, fmt.Errorf("session: branch: parent %q not found", parentID)
	}

	// Clamp the branch point to the parent's history length.
	cut := atTurn
	if cut > len(parent.Messages) {
		cut = len(parent.Messages)
	}

	now := time.Now().UTC()
	prefix := make([]providers.Message, cut)
	copy(prefix, parent.Messages[:cut])

	child := &Session{
		ID:             uuid.New().String(),
		Title:          "branch of " + titleOr(parent.Title, "session"),
		Folder:         parent.Folder,
		ParentID:       parent.ID,
		CreatedAt:      now,
		UpdatedAt:      now,
		Model:          parent.Model,
		Provider:       parent.Provider,
		Status:         "active",
		Metadata:       copyMetadata(parent.Metadata),
		Messages:       prefix,
		TurnCount:      cut,
		BranchedAtTurn: cut,
	}
	if err := s.Create(ctx, child); err != nil {
		return nil, fmt.Errorf("session: branch: create child: %w", err)
	}

	// Append to parent's Children list and persist. Best-effort: if
	// the parent save fails, the child still exists — the caller can
	// retry the parent update or live with a one-way link.
	parent.Children = append(parent.Children, child.ID)
	parent.UpdatedAt = now
	if err := s.Save(ctx, parent); err != nil {
		return child, fmt.Errorf("session: branch: update parent children: %w", err)
	}
	return child, nil
}

// copyMetadata returns a shallow copy of m, never nil. Used by Branch
// to avoid sharing the parent's metadata map with the child.
func copyMetadata(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

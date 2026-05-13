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

// Clone creates an independent duplicate of parentID with the full
// message history copied over. Semantically a Branch from the END, but
// the intent is different enough to surface as its own method: Branch
// says "diverge from here forward"; Clone says "I want a backup before
// trying something risky in the original".
//
// The returned session has a fresh ID, a "clone of <title>" title, and
// is linked into the parent's Children list so the session tree stays
// consistent. The parent is NOT modified beyond the Children append.
func (s *BadgerStore) Clone(ctx context.Context, parentID string) (*Session, error) {
	if parentID == "" {
		return nil, fmt.Errorf("session: clone: parentID is required")
	}
	parent, err := s.Load(ctx, parentID)
	if err != nil {
		return nil, fmt.Errorf("session: clone: load parent: %w", err)
	}
	if parent == nil {
		return nil, fmt.Errorf("session: clone: parent %q not found", parentID)
	}

	now := time.Now().UTC()
	full := make([]providers.Message, len(parent.Messages))
	copy(full, parent.Messages)

	child := &Session{
		ID:             uuid.New().String(),
		Title:          "clone of " + titleOr(parent.Title, "session"),
		Folder:         parent.Folder,
		ParentID:       parent.ID,
		CreatedAt:      now,
		UpdatedAt:      now,
		Model:          parent.Model,
		Provider:       parent.Provider,
		Status:         "active",
		Metadata:       copyMetadata(parent.Metadata),
		Messages:       full,
		TurnCount:      len(full),
		BranchedAtTurn: len(full),
	}
	if err := s.Create(ctx, child); err != nil {
		return nil, fmt.Errorf("session: clone: create child: %w", err)
	}

	parent.Children = append(parent.Children, child.ID)
	parent.UpdatedAt = now
	if err := s.Save(ctx, parent); err != nil {
		// Child exists; parent link failed. Same best-effort policy as
		// Branch — surface the error so the caller can retry the link
		// or accept the one-way reference.
		return child, fmt.Errorf("session: clone: update parent children: %w", err)
	}
	return child, nil
}

// ErrMergeDiverged is returned by Merge when the parent has accumulated
// messages past the child's branch point. The user must resolve the
// conflict (cherry-pick, manual merge, or discard one side) — we don't
// attempt a 3-way merge of LLM conversations because there's no clean
// "diff" semantics to fall back on.
var ErrMergeDiverged = fmt.Errorf("session: merge: parent diverged past branch point")

// Merge fast-forwards a child branch back into its parent. Conditions:
//
//   - Child must have a non-empty ParentID
//   - Parent must still have exactly BranchedAtTurn messages (no
//     divergence). If the parent grew after the branch, returns
//     ErrMergeDiverged.
//   - The parent absorbs child.Messages[BranchedAtTurn:] — i.e. the
//     turns the child accumulated after branching.
//
// The child session is NOT deleted. Callers who want to garbage-collect
// the merged-back branch can delete it explicitly. We don't auto-delete
// because the child's history is still a valid snapshot — keeping it
// preserves the "I tried this and it worked" audit trail.
//
// Returns the updated parent.
func (s *BadgerStore) Merge(ctx context.Context, childID string) (*Session, error) {
	if childID == "" {
		return nil, fmt.Errorf("session: merge: childID is required")
	}
	child, err := s.Load(ctx, childID)
	if err != nil {
		return nil, fmt.Errorf("session: merge: load child: %w", err)
	}
	if child == nil {
		return nil, fmt.Errorf("session: merge: child %q not found", childID)
	}
	if child.ParentID == "" {
		return nil, fmt.Errorf("session: merge: child %q has no parent", childID)
	}
	parent, err := s.Load(ctx, child.ParentID)
	if err != nil {
		return nil, fmt.Errorf("session: merge: load parent: %w", err)
	}
	if parent == nil {
		return nil, fmt.Errorf("session: merge: parent %q not found", child.ParentID)
	}

	branchPoint := child.BranchedAtTurn
	if branchPoint < 0 || branchPoint > len(child.Messages) {
		return nil, fmt.Errorf("session: merge: child %q has invalid BranchedAtTurn=%d", childID, branchPoint)
	}
	// Divergence check: parent must still be at the branch point.
	// Allowing > would mean the parent had its own new conversation
	// the child doesn't know about; merging would silently drop those
	// turns. Allowing < shouldn't happen (the branch took a prefix),
	// but we guard anyway so a corrupted store doesn't crash.
	if len(parent.Messages) != branchPoint {
		return nil, ErrMergeDiverged
	}

	tail := child.Messages[branchPoint:]
	if len(tail) == 0 {
		// Nothing to merge — child made no progress past the branch.
		// Treat as a no-op rather than an error; the user gets the
		// parent back unchanged.
		return parent, nil
	}

	now := time.Now().UTC()
	parent.Messages = append(parent.Messages, tail...)
	parent.TurnCount = len(parent.Messages)
	parent.UpdatedAt = now
	if err := s.Save(ctx, parent); err != nil {
		return nil, fmt.Errorf("session: merge: save parent: %w", err)
	}
	return parent, nil
}

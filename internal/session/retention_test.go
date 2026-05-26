package session

import (
	"context"
	"testing"
	"time"
)

func TestEnforceMax_NoCap(t *testing.T) {
	store, err := NewBadgerStore(t.TempDir())
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	defer store.Close()
	for i := 0; i < 5; i++ {
		_ = store.Create(context.Background(), NewSession("/tmp"))
	}
	deleted, err := EnforceMax(context.Background(), store, 0)
	if err != nil {
		t.Fatalf("EnforceMax: %v", err)
	}
	if deleted != 0 {
		t.Errorf("max=0 should not delete, got %d", deleted)
	}
}

func TestEnforceMax_PrunesOldest(t *testing.T) {
	store, err := NewBadgerStore(t.TempDir())
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	defer store.Close()
	now := time.Now().UTC()
	for i := 0; i < 6; i++ {
		s := NewSession("/tmp")
		s.UpdatedAt = now.Add(time.Duration(i) * time.Minute)
		_ = store.Create(context.Background(), s)
		// Save once so the UpdatedAt persists (Create may stamp now).
		_ = store.Save(context.Background(), s)
	}
	deleted, err := EnforceMax(context.Background(), store, 3)
	if err != nil {
		t.Fatalf("EnforceMax: %v", err)
	}
	if deleted != 3 {
		t.Errorf("expected 3 deletes, got %d", deleted)
	}
	remaining, err := store.List(context.Background(), ListOptions{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(remaining) != 3 {
		t.Errorf("expected 3 remaining, got %d", len(remaining))
	}
}

func TestEnforceMax_SkipsSubSessions(t *testing.T) {
	store, err := NewBadgerStore(t.TempDir())
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	defer store.Close()
	parent := NewSession("/tmp")
	_ = store.Create(context.Background(), parent)
	for i := 0; i < 3; i++ {
		sub := NewSession("/tmp")
		sub.ParentID = parent.ID
		_ = store.Create(context.Background(), sub)
	}
	// Cap of 1, but only the parent counts → no prunes.
	deleted, err := EnforceMax(context.Background(), store, 1)
	if err != nil {
		t.Fatalf("EnforceMax: %v", err)
	}
	if deleted != 0 {
		t.Errorf("sub-sessions should not count toward cap; got %d deletes", deleted)
	}
}

func TestEnforceMax_NilStore(t *testing.T) {
	deleted, err := EnforceMax(context.Background(), nil, 5)
	if err != nil || deleted != 0 {
		t.Errorf("nil store should be a no-op; got deleted=%d err=%v", deleted, err)
	}
}

package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
)

// newForker returns a forker function backed by a real PostgresStore plus
// the store reference so callers can inspect session state.
func newForker(t *testing.T) (func(ctx context.Context, parentID, name string) (string, error), *session.PostgresStore) {
	t.Helper()
	store := session.NewPostgresStore(openTestDB(t))

	t.Cleanup(func() { store.Close() })

	fn := func(ctx context.Context, parentID, name string) (string, error) {
		child, err := store.Clone(ctx, parentID)
		if err != nil {
			return "", err
		}
		if name != "" {
			child.Title = name
			_ = store.Save(ctx, child)
		}
		return child.ID, nil
	}
	return fn, store
}

// seedSession creates a parent session with n alternating user/assistant
// messages and persists it. Returns the session.
func seedSession(t *testing.T, store *session.PostgresStore, n int) *session.Session {
	t.Helper()
	s := session.NewSession("/repo")
	s.Title = "trunk"
	s.Model = "test-model"
	for i := 0; i < n; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		s.Messages = append(s.Messages, providers.Message{Role: role, Content: "msg " + itoa(i)})
	}
	s.TurnCount = n
	if err := store.Create(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestFork_CreatesNewSession(t *testing.T) {
	forkFn, store := newForker(t)
	parent := seedSession(t, store, 4)

	ag := newTestAgent(&mockProvider{}, nil, nil, nil)
	ag.SetSessionID(parent.ID)
	ag.SetForker(forkFn)

	newID, err := ag.Fork("my-fork")
	if err != nil {
		t.Fatalf("Fork(): %v", err)
	}
	if newID == "" {
		t.Error("Fork() returned empty session ID")
	}
	if newID == parent.ID {
		t.Errorf("Fork() returned parent ID %q, want a different ID", newID)
	}
}

func TestFork_HistoryPreserved(t *testing.T) {
	forkFn, store := newForker(t)
	parent := seedSession(t, store, 4)

	ag := newTestAgent(&mockProvider{}, nil, nil, nil)
	ag.SetSessionID(parent.ID)
	ag.SetForker(forkFn)

	newID, err := ag.Fork("my-fork")
	if err != nil {
		t.Fatalf("Fork(): %v", err)
	}

	child, err := store.Load(context.Background(), newID)
	if err != nil {
		t.Fatalf("Load child: %v", err)
	}
	if len(child.Messages) != 4 {
		t.Errorf("child should have 4 messages, got %d", len(child.Messages))
	}
	for i, m := range child.Messages {
		if m.Content != "msg "+itoa(i) {
			t.Errorf("child message[%d] = %q, want %q", i, m.Content, "msg "+itoa(i))
		}
	}
}

func TestFork_ParentID(t *testing.T) {
	forkFn, store := newForker(t)
	parent := seedSession(t, store, 3)

	ag := newTestAgent(&mockProvider{}, nil, nil, nil)
	ag.SetSessionID(parent.ID)
	ag.SetForker(forkFn)

	newID, err := ag.Fork("my-fork")
	if err != nil {
		t.Fatalf("Fork(): %v", err)
	}

	child, err := store.Load(context.Background(), newID)
	if err != nil {
		t.Fatalf("Load child: %v", err)
	}
	if child.ParentID != parent.ID {
		t.Errorf("child ParentID = %q, want %q", child.ParentID, parent.ID)
	}
}

func TestFork_IndependentModifications(t *testing.T) {
	forkFn, store := newForker(t)
	parent := seedSession(t, store, 2)

	ag := newTestAgent(&mockProvider{}, nil, nil, nil)
	ag.SetSessionID(parent.ID)
	ag.SetForker(forkFn)

	newID, err := ag.Fork("my-fork")
	if err != nil {
		t.Fatalf("Fork(): %v", err)
	}

	// Mutate child — add a message.
	child, _ := store.Load(context.Background(), newID)
	child.Messages = append(child.Messages, providers.Message{Role: "user", Content: "child-only"})
	if err := store.Save(context.Background(), child); err != nil {
		t.Fatal(err)
	}

	// Parent must stay untouched.
	reloaded, _ := store.Load(context.Background(), parent.ID)
	if len(reloaded.Messages) != 2 {
		t.Errorf("parent must not see child mutations: got %d messages", len(reloaded.Messages))
	}
}

func TestFork_NoForkerConfigured(t *testing.T) {
	ag := newTestAgent(&mockProvider{}, nil, nil, nil)
	ag.SetSessionID("some-session")

	_, err := ag.Fork("test")
	if err == nil {
		t.Error("Fork() with no forker should error")
	}
	if !strings.Contains(err.Error(), "no session store") {
		t.Errorf("Fork() error = %q, want 'no session store'", err.Error())
	}
}

func TestFork_NoActiveSession(t *testing.T) {
	forkFn, store := newForker(t)
	_ = store

	ag := newTestAgent(&mockProvider{}, nil, nil, nil)
	ag.SetForker(forkFn)
	// Clear the session ID to simulate no active session.
	ag.SetSessionID("")

	_, err := ag.Fork("test")
	if err == nil {
		t.Error("Fork() with empty session ID should error")
	}
	if !strings.Contains(err.Error(), "no active session") {
		t.Errorf("Fork() error = %q, want 'no active session'", err.Error())
	}
}

func TestFork_CustomName(t *testing.T) {
	forkFn, store := newForker(t)
	parent := seedSession(t, store, 2)

	ag := newTestAgent(&mockProvider{}, nil, nil, nil)
	ag.SetSessionID(parent.ID)
	ag.SetForker(forkFn)

	newID, err := ag.Fork("experimental-branch")
	if err != nil {
		t.Fatalf("Fork(): %v", err)
	}

	child, err := store.Load(context.Background(), newID)
	if err != nil {
		t.Fatalf("Load child: %v", err)
	}
	if child.Title != "experimental-branch" {
		t.Errorf("child Title = %q, want 'experimental-branch'", child.Title)
	}
}

package gateway

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

func TestDispatch_BookmarkCommand_PersistsLabel(t *testing.T) {
	router, _ := NewSessionRouter("")
	d := NewDispatcher(&fakeAgent{}, router)

	var mu sync.Mutex
	calls := []bookmarkCall{}
	d.Bookmark = func(ctx context.Context, sessionID, label string) error {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, bookmarkCall{sessionID, label})
		return nil
	}

	reply := &fakeReply{}
	d.Handle(context.Background(), Inbound{
		Channel: "telegram", ChatKey: "user-42",
		Text: "/bm payment-bug-repro",
	}, reply)

	final := waitFinal(t, reply)
	if !strings.Contains(final, "bookmarked") {
		t.Errorf("reply should confirm bookmark, got %q", final)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 1 {
		t.Fatalf("want 1 bookmark call, got %d", len(calls))
	}
	if calls[0].label != "payment-bug-repro" {
		t.Errorf("label: %q", calls[0].label)
	}
	if calls[0].sessionID == "" {
		t.Error("session id should be resolved (non-empty)")
	}
}

func TestDispatch_BookmarkCommand_AliasBookmark(t *testing.T) {
	router, _ := NewSessionRouter("")
	d := NewDispatcher(&fakeAgent{}, router)
	var fired bool
	d.Bookmark = func(_ context.Context, _, _ string) error {
		fired = true
		return nil
	}
	reply := &fakeReply{}
	d.Handle(context.Background(), Inbound{
		Channel: "telegram", ChatKey: "u",
		Text: "/bookmark long-form alias works",
	}, reply)
	_ = waitFinal(t, reply)
	if !fired {
		t.Error("/bookmark alias should hit the same handler")
	}
}

func TestDispatch_BookmarkCommand_RequiresLabel(t *testing.T) {
	router, _ := NewSessionRouter("")
	d := NewDispatcher(&fakeAgent{}, router)
	called := false
	d.Bookmark = func(_ context.Context, _, _ string) error {
		called = true
		return nil
	}
	reply := &fakeReply{}
	d.Handle(context.Background(), Inbound{
		Channel: "telegram", ChatKey: "u", Text: "/bm",
	}, reply)
	final := waitFinal(t, reply)
	if !strings.Contains(final, "usage") {
		t.Errorf("missing-label should show usage: %q", final)
	}
	if called {
		t.Error("backend should NOT fire when no label given")
	}
}

func TestDispatch_BookmarkCommand_NoBackendWired(t *testing.T) {
	// Dispatcher.Bookmark is nil — user should see a clear error,
	// not silent success.
	router, _ := NewSessionRouter("")
	d := NewDispatcher(&fakeAgent{}, router)
	reply := &fakeReply{}
	d.Handle(context.Background(), Inbound{
		Channel: "telegram", ChatKey: "u", Text: "/bm whatever",
	}, reply)
	final := waitFinal(t, reply)
	if !strings.Contains(final, "not wired") && !strings.Contains(final, "backend") {
		t.Errorf("no-backend message should be informative, got %q", final)
	}
}

func TestDispatch_BookmarkCommand_BackendErrorSurfaces(t *testing.T) {
	router, _ := NewSessionRouter("")
	d := NewDispatcher(&fakeAgent{}, router)
	d.Bookmark = func(_ context.Context, _, _ string) error {
		return errors.New("disk full")
	}
	reply := &fakeReply{}
	d.Handle(context.Background(), Inbound{
		Channel: "telegram", ChatKey: "u", Text: "/bm test",
	}, reply)
	final := waitFinal(t, reply)
	if !strings.Contains(final, "disk full") {
		t.Errorf("backend error should surface verbatim: %q", final)
	}
}

func TestDispatch_HelpListsBookmark(t *testing.T) {
	d := NewDispatcher(&fakeAgent{}, nil)
	reply := &fakeReply{}
	d.Handle(context.Background(), Inbound{Channel: "x", ChatKey: "1", Text: "/help"}, reply)
	final := waitFinal(t, reply)
	if !strings.Contains(final, "/bm") {
		t.Errorf("help text should mention /bm: %q", final)
	}
}

type bookmarkCall struct {
	sessionID string
	label     string
}

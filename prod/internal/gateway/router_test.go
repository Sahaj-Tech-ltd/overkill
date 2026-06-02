package gateway

import (
	"path/filepath"
	"testing"
)

func TestSessionRouter_BindResolve(t *testing.T) {
	r, err := NewSessionRouter(filepath.Join(t.TempDir(), "r.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Bind("telegram", "12345", "", "sess-1"); err != nil {
		t.Fatal(err)
	}
	got, follow := r.Resolve("telegram", "12345", "", "tui-active")
	if got != "sess-1" || follow {
		t.Fatalf("got %q follow=%v want sess-1 follow=false", got, follow)
	}
}

func TestSessionRouter_FollowTUI(t *testing.T) {
	r, _ := NewSessionRouter("")
	_ = r.Bind("telegram", "12345", "", "sess-1")
	if err := r.Follow("telegram", "12345", "tui"); err != nil {
		t.Fatal(err)
	}
	got, follow := r.Resolve("telegram", "12345", "", "tui-live")
	if got != "tui-live" || !follow {
		t.Fatalf("follow tui: got %q follow=%v", got, follow)
	}
	// Empty live id falls through to the binding.
	got, _ = r.Resolve("telegram", "12345", "", "")
	if got != "sess-1" {
		t.Fatalf("fallthrough: got %q want sess-1", got)
	}
}

func TestSessionRouter_FollowPinned(t *testing.T) {
	r, _ := NewSessionRouter("")
	_ = r.Follow("discord", "chat-x", "pinned-sess")
	got, follow := r.Resolve("discord", "chat-x", "", "tui-live")
	if got != "pinned-sess" || !follow {
		t.Fatalf("pin: got %q follow=%v", got, follow)
	}
	_ = r.Follow("discord", "chat-x", "")
	if r.FollowTarget("discord", "chat-x") != "" {
		t.Fatal("follow not cleared")
	}
}

func TestSessionRouter_RecentSorted(t *testing.T) {
	r, _ := NewSessionRouter("")
	_ = r.Bind("telegram", "a", "", "s1")
	_ = r.Bind("telegram", "b", "", "s2")
	_ = r.Bind("telegram", "c", "", "s3")
	rows := r.Recent(2)
	if len(rows) != 2 {
		t.Fatalf("limit: got %d want 2", len(rows))
	}
	// Most recently bound is c (s3).
	if rows[0].SessionID != "s3" {
		t.Fatalf("first row %q want s3", rows[0].SessionID)
	}
}

func TestSessionRouter_Persistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "r.json")
	r1, _ := NewSessionRouter(path)
	_ = r1.Bind("telegram", "9", "", "sess-99")
	_ = r1.Follow("telegram", "9", "tui")

	r2, err := NewSessionRouter(path)
	if err != nil {
		t.Fatal(err)
	}
	got, follow := r2.Resolve("telegram", "9", "", "live-7")
	if got != "live-7" || !follow {
		t.Fatalf("after reload: got %q follow=%v", got, follow)
	}
}

func TestNewSessionID_HasPrefix(t *testing.T) {
	id := NewSessionID("telegram")
	if len(id) < len("telegram-")+4 || id[:len("telegram-")] != "telegram-" {
		t.Fatalf("bad id: %q", id)
	}
}

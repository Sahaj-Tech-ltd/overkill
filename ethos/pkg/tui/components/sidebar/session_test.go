package sidebar

import (
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/ethos/internal/session"
)

func TestSessionPanel_Render(t *testing.T) {
	p := NewSessionPanel()
	s := &session.Session{ID: "1", Title: "test session", CreatedAt: time.Now(), TurnCount: 5}
	p.SetSessions([]*session.Session{s})
	p.SetCurrent("1")
	v := p.View(30, 15)
	if !containsStr(v, "test session") {
		t.Error("should show session title")
	}
}

func TestSessionPanel_ListSessions(t *testing.T) {
	p := NewSessionPanel()
	sessions := []*session.Session{
		{ID: "1", Title: "a", CreatedAt: time.Now(), UpdatedAt: time.Now()},
		{ID: "2", Title: "b", CreatedAt: time.Now(), UpdatedAt: time.Now()},
		{ID: "3", Title: "c", CreatedAt: time.Now(), UpdatedAt: time.Now()},
	}
	p.SetSessions(sessions)
	v := p.View(30, 15)
	if !containsStr(v, "a") || !containsStr(v, "b") || !containsStr(v, "c") {
		t.Error("missing sessions")
	}
}

func TestSessionPanel_Empty(t *testing.T) {
	p := NewSessionPanel()
	v := p.View(30, 15)
	if !containsStr(v, "No sessions") {
		t.Error("should show empty state")
	}
}

func TestSessionPanel_Truncate(t *testing.T) {
	result := truncate("this-is-a-very-long-session-name-that-should-truncate", 20)
	if len(result) > 20 {
		t.Errorf("expected <=20, got %d", len(result))
	}
}

func TestSessionPanel_FormatTime(t *testing.T) {
	now := time.Now()
	cases := []struct {
		offset time.Duration
		want   string
	}{
		{30 * time.Second, "just now"},
		{5 * time.Minute, "5m ago"},
		{2 * time.Hour, "2h ago"},
		{3 * 24 * time.Hour, "3d ago"},
	}
	for _, c := range cases {
		got := formatRelativeTime(now.Add(-c.offset))
		if !containsStr(got, c.want) {
			t.Errorf("expected %q in %q", c.want, got)
		}
	}
}

func TestSessionPanel_MessageCount(t *testing.T) {
	p := NewSessionPanel()
	s := &session.Session{ID: "1", Title: "test", CreatedAt: time.Now(), TurnCount: 5}
	p.SetSessions([]*session.Session{s})
	p.SetCurrent("1")
	v := p.View(30, 15)
	if !containsStr(v, "5") {
		t.Error("should show turn count")
	}
}

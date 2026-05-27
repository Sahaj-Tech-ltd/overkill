package dialog

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
)

func TestSessionSwitcher_Show(t *testing.T) {
	s := NewSessionDialog()
	updated, _ := s.Update(ShowSessionDialogMsg{})
	if !updated.Show {
		t.Error("ShowSessionDialogMsg should set Show=true")
	}
}

func TestSessionSwitcher_List(t *testing.T) {
	s := NewSessionDialog()
	s.SetSessions([]*session.Session{
		{ID: "1", Title: "Chat about Go", UpdatedAt: time.Now(), TurnCount: 5},
		{ID: "2", Title: "Debug session", UpdatedAt: time.Now().Add(-time.Hour), TurnCount: 3},
		{ID: "3", Title: "Refactor code", UpdatedAt: time.Now().Add(-2 * time.Hour), TurnCount: 10},
	})
	s.Show = true
	v := s.View(80, 24)
	for _, title := range []string{"Chat about Go", "Debug session", "Refactor code"} {
		if !strings.Contains(v, title) {
			t.Errorf("view should contain %q", title)
		}
	}
}

func TestSessionSwitcher_Select(t *testing.T) {
	s := NewSessionDialog()
	s.SetSessions([]*session.Session{
		{ID: "abc-123", Title: "Test", UpdatedAt: time.Now(), TurnCount: 1},
	})
	s.Show = true
	_, cmd := s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter should return a command")
	}
	msg := cmd()
	sel, ok := msg.(SessionSelectedMsg)
	if !ok {
		t.Fatal("command should return SessionSelectedMsg")
	}
	if sel.Session.ID != "abc-123" {
		t.Errorf("expected session ID abc-123, got %q", sel.Session.ID)
	}
}

func TestSessionSwitcher_Highlight(t *testing.T) {
	s := NewSessionDialog()
	s.SetSessions([]*session.Session{
		{ID: "1", Title: "First", UpdatedAt: time.Now(), TurnCount: 1},
		{ID: "2", Title: "Second", UpdatedAt: time.Now().Add(-time.Hour), TurnCount: 2},
	})
	s.Show = true
	updated, _ := s.Update(tea.KeyMsg{Type: tea.KeyDown})
	v := updated.View(80, 24)
	lines := strings.Split(v, "\n")
	found := false
	for _, line := range lines {
		if strings.Contains(line, ">") && strings.Contains(line, "Second") {
			found = true
			break
		}
	}
	if !found {
		t.Error("cursor should highlight second session with >")
	}
}

func TestSessionSwitcher_Empty(t *testing.T) {
	s := NewSessionDialog()
	s.Show = true
	v := s.View(80, 24)
	if !strings.Contains(v, "No sessions") {
		t.Error("empty dialog should show 'No sessions'")
	}
}

func TestSessionSwitcher_Close(t *testing.T) {
	s := NewSessionDialog()
	s.Show = true
	_, cmd := s.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("esc should return a command")
	}
	msg := cmd()
	if _, ok := msg.(CloseSessionDialogMsg); !ok {
		t.Error("esc should return CloseSessionDialogMsg")
	}
}

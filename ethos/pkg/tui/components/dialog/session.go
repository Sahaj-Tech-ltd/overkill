package dialog

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Sahaj-Tech-ltd/ethos/internal/session"
)

type ShowSessionDialogMsg struct{}
type CloseSessionDialogMsg struct{}
type SessionSelectedMsg struct{ Session *session.Session }

// SessionRenameRequestMsg is emitted when the user presses 'r' in the list.
type SessionRenameRequestMsg struct{ Session *session.Session }

// SessionDeleteRequestMsg is emitted when the user presses 'd' in the list.
type SessionDeleteRequestMsg struct{ Session *session.Session }

// SessionNewRequestMsg is emitted when the user presses 'n' in the list.
type SessionNewRequestMsg struct{}

type SessionDialog struct {
	Dialog
	Sessions   []*session.Session
	Cursor     int
	SelectedID string
}

func NewSessionDialog() SessionDialog {
	return SessionDialog{Dialog: Dialog{Title: "Sessions"}}
}

func (s *SessionDialog) SetSessions(sessions []*session.Session) {
	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})
	s.Sessions = sessions
	s.Cursor = 0
}

func (s SessionDialog) Update(msg tea.Msg) (SessionDialog, tea.Cmd) {
	switch k := msg.(type) {
	case tea.KeyMsg:
		switch k.String() {
		case "up":
			if s.Cursor > 0 {
				s.Cursor--
			}
		case "down":
			if s.Cursor < len(s.Sessions)-1 {
				s.Cursor++
			}
		case "enter":
			if s.Cursor < len(s.Sessions) {
				sel := s.Sessions[s.Cursor]
				return s, func() tea.Msg { return SessionSelectedMsg{Session: sel} }
			}
		case "r":
			if s.Cursor < len(s.Sessions) {
				return s, func() tea.Msg { return SessionRenameRequestMsg{Session: s.Sessions[s.Cursor]} }
			}
		case "d":
			if s.Cursor < len(s.Sessions) {
				return s, func() tea.Msg { return SessionDeleteRequestMsg{Session: s.Sessions[s.Cursor]} }
			}
		case "n":
			return s, func() tea.Msg { return SessionNewRequestMsg{} }
		case "esc":
			return s, func() tea.Msg { return CloseSessionDialogMsg{} }
		}
	case ShowSessionDialogMsg:
		s.Show = true
	case CloseSessionDialogMsg:
		s.Show = false
		s.Cursor = 0
	}
	return s, nil
}

func (s SessionDialog) View(totalWidth, totalHeight int) string {
	if !s.Show {
		return ""
	}
	if len(s.Sessions) == 0 {
		return s.BaseView("No sessions available", totalWidth, totalHeight)
	}
	var lines []string
	for i, sess := range s.Sessions {
		name := sess.Title
		if len(name) > 30 {
			name = name[:27] + "..."
		}
		ago := formatRelativeTime(sess.UpdatedAt)
		prefix := "  "
		if i == s.Cursor {
			prefix = "> "
		}
		lines = append(lines, fmt.Sprintf("%s%s - %s - %d msgs", prefix, name, ago, sess.TurnCount))
	}
	lines = append(lines, "")
	lines = append(lines, "enter: switch · n: new · r: rename · d: delete · esc")
	content := strings.Join(lines, "\n")
	return s.BaseView(content, totalWidth, totalHeight)
}

func formatRelativeTime(t time.Time) string {
	d := time.Since(t)
	if d < time.Minute {
		return "just now"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

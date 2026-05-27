package dialog

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/Sahaj-Tech-ltd/overkill/internal/session"
)

// SessionRenamedMsg is emitted with the new title (or canceled if empty).
type SessionRenamedMsg struct {
	Session *session.Session
	Title   string
	Cancel  bool
}

// SessionDeletedMsg is emitted after the user confirms a delete.
type SessionDeletedMsg struct {
	Session *session.Session
}

// CloseRenameDialogMsg dismisses the rename overlay.
type CloseRenameDialogMsg struct{}

// SessionRenameDialog is a single-line text-input prompt.
type SessionRenameDialog struct {
	Dialog
	Session *session.Session
	Buffer  string
}

// NewSessionRenameDialog returns a fresh hidden dialog.
func NewSessionRenameDialog() SessionRenameDialog {
	return SessionRenameDialog{Dialog: Dialog{Title: "rename session"}}
}

// SetSession seeds the dialog with the current title.
func (d *SessionRenameDialog) SetSession(s *session.Session) {
	d.Session = s
	if s != nil {
		d.Buffer = s.Title
	}
	d.Show = true
}

// Update handles text input and submit/cancel.
func (d SessionRenameDialog) Update(msg tea.Msg) (SessionRenameDialog, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return d, nil
	}
	switch k.String() {
	case "esc":
		d.Show = false
		return d, func() tea.Msg { return CloseRenameDialogMsg{} }
	case "enter":
		d.Show = false
		return d, func() tea.Msg {
			return SessionRenamedMsg{Session: d.Session, Title: d.Buffer}
		}
	case "backspace":
		if len(d.Buffer) > 0 {
			d.Buffer = d.Buffer[:len(d.Buffer)-1]
		}
	default:
		if k.Type == tea.KeyRunes {
			d.Buffer += string(k.Runes)
		} else if k.Type == tea.KeySpace {
			d.Buffer += " "
		}
	}
	return d, nil
}

// View renders the input box.
func (d SessionRenameDialog) View(totalWidth, totalHeight int) string {
	if !d.Show {
		return ""
	}
	body := "new title:\n  " + d.Buffer + "▎\n\nenter to save · esc to cancel"
	return d.BaseView(body, totalWidth, totalHeight)
}

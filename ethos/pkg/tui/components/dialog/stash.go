// Package dialog — prompt stash list overlay.
package dialog

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Sahaj-Tech-ltd/ethos/internal/session"
	"github.com/Sahaj-Tech-ltd/ethos/pkg/tui/theme"
)

// StashSelectedMsg is emitted when the user picks a stash entry. The receiver
// is responsible for inserting it into the editor and (optionally) deleting it
// from storage.
type StashSelectedMsg struct {
	Entry session.StashEntry
}

// CloseStashDialogMsg is emitted on Esc.
type CloseStashDialogMsg struct{}

// StashDialog renders the saved-prompt list.
type StashDialog struct {
	Dialog
	entries []session.StashEntry
	cursor  int
}

// NewStashDialog returns a fresh, hidden dialog.
func NewStashDialog() StashDialog {
	return StashDialog{Dialog: Dialog{Title: "stashed prompts"}}
}

// SetEntries replaces the entries list and resets the cursor.
func (s *StashDialog) SetEntries(entries []session.StashEntry) {
	s.entries = append([]session.StashEntry(nil), entries...)
	s.cursor = 0
}

// Entries returns the current entries (read-only).
func (s StashDialog) Entries() []session.StashEntry { return s.entries }

// Cursor returns the highlight index.
func (s StashDialog) Cursor() int { return s.cursor }

// Update handles dialog navigation.
func (s StashDialog) Update(msg tea.Msg) (StashDialog, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return s, nil
	}
	switch k.String() {
	case "up", "k":
		if s.cursor > 0 {
			s.cursor--
		}
	case "down", "j":
		if s.cursor < len(s.entries)-1 {
			s.cursor++
		}
	case "enter":
		if len(s.entries) == 0 {
			return s, nil
		}
		entry := s.entries[s.cursor]
		s.Show = false
		return s, func() tea.Msg { return StashSelectedMsg{Entry: entry} }
	case "esc":
		s.Show = false
		return s, func() tea.Msg { return CloseStashDialogMsg{} }
	}
	return s, nil
}

// View renders the dialog.
func (s StashDialog) View(totalWidth, totalHeight int) string {
	if !s.Show {
		return ""
	}
	t := theme.CurrentTheme()
	hi := lipgloss.NewStyle().Foreground(t.Background()).Background(t.Accent()).Bold(true)
	row := lipgloss.NewStyle().Foreground(t.Text())
	muted := lipgloss.NewStyle().Foreground(t.TextMuted())

	if len(s.entries) == 0 {
		return s.BaseView("(no stashed prompts)\n\nUse /stash to save a draft.", totalWidth, totalHeight)
	}
	var b strings.Builder
	max := len(s.entries)
	if max > 12 {
		max = 12
	}
	for i := 0; i < max; i++ {
		e := s.entries[i]
		preview := strings.ReplaceAll(e.Text, "\n", " ")
		if len(preview) > 60 {
			preview = preview[:57] + "..."
		}
		ts := muted.Render(humanizeAge(e.SavedAt))
		line := fmt.Sprintf("%s  %s", preview, ts)
		if i == s.cursor {
			b.WriteString(hi.Render("> " + line))
		} else {
			b.WriteString(row.Render("  " + line))
		}
		b.WriteString("\n")
	}
	b.WriteString(muted.Render("\nenter: insert  ·  esc: close"))
	return s.BaseView(strings.TrimRight(b.String(), "\n"), totalWidth, totalHeight)
}

func humanizeAge(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

package dialog

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Sahaj-Tech-ltd/ethos/internal/providers"
)

// TimelineForkMsg asks the agent to truncate history to keep only the first N
// messages (everything after that index is dropped).
type TimelineForkMsg struct {
	KeepCount int
}

// CloseTimelineDialogMsg dismisses the dialog.
type CloseTimelineDialogMsg struct{}

// TimelineDialog lets the user pick a past message to fork from.
type TimelineDialog struct {
	Dialog
	Messages []providers.Message
	Cursor   int
}

// NewTimelineDialog returns a fresh dialog (hidden by default).
func NewTimelineDialog() TimelineDialog {
	return TimelineDialog{Dialog: Dialog{Title: "fork from message"}}
}

// SetMessages seeds the dialog with the current history.
func (d *TimelineDialog) SetMessages(history []providers.Message) {
	d.Messages = history
	d.Cursor = 0
	d.Show = true
}

// Update handles cursor movement and selection.
func (d TimelineDialog) Update(msg tea.Msg) (TimelineDialog, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return d, nil
	}
	switch k.String() {
	case "esc":
		d.Show = false
		return d, func() tea.Msg { return CloseTimelineDialogMsg{} }
	case "up", "k":
		if d.Cursor > 0 {
			d.Cursor--
		}
	case "down", "j":
		if d.Cursor < len(d.Messages)-1 {
			d.Cursor++
		}
	case "enter":
		// keep messages [0..Cursor] inclusive
		keep := d.Cursor + 1
		d.Show = false
		return d, func() tea.Msg { return TimelineForkMsg{KeepCount: keep} }
	}
	return d, nil
}

// View renders the message list.
func (d TimelineDialog) View(totalWidth, totalHeight int) string {
	if !d.Show {
		return ""
	}
	if len(d.Messages) == 0 {
		return d.BaseView("no messages yet", totalWidth, totalHeight)
	}
	var b strings.Builder
	for i, m := range d.Messages {
		prefix := "  "
		if i == d.Cursor {
			prefix = "> "
		}
		preview := strings.ReplaceAll(m.Content, "\n", " ")
		if len(preview) > 60 {
			preview = preview[:57] + "..."
		}
		fmt.Fprintf(&b, "%s[%s] %s\n", prefix, m.Role, preview)
	}
	b.WriteString("\nenter to fork at selection · esc to cancel")
	return d.BaseView(strings.TrimRight(b.String(), "\n"), totalWidth, totalHeight)
}

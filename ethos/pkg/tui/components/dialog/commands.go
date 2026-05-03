package dialog

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Sahaj-Tech-ltd/ethos/pkg/tui/theme"
)

type Command struct {
	ID          string
	Title       string
	Description string
	Handler     func(Command) tea.Cmd
}

type CommandDialog struct {
	Dialog
	Commands []Command
	Filtered []Command
	Cursor   int
	Query    string
}

type ShowCommandDialogMsg struct{}
type CloseCommandDialogMsg struct{}
type CommandSelectedMsg struct{ Command Command }

func NewCommandDialog() CommandDialog {
	return CommandDialog{Dialog: Dialog{Title: "Commands"}}
}

func (c *CommandDialog) RegisterCommand(cmd Command) {
	c.Commands = append(c.Commands, cmd)
	c.Filtered = c.Commands
}

func (c *CommandDialog) Update(msg tea.Msg) (CommandDialog, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up":
			if c.Cursor > 0 {
				c.Cursor--
			}
		case "down":
			if c.Cursor < len(c.Filtered)-1 {
				c.Cursor++
			}
		case "enter", "\r":
			if c.Cursor < len(c.Filtered) {
				sel := c.Filtered[c.Cursor]
				if sel.Handler != nil {
					return *c, sel.Handler(sel)
				}
				return *c, func() tea.Msg { return CommandSelectedMsg{Command: sel} }
			}
		case "esc":
			c.Show = false
			c.Query = ""
			c.Cursor = 0
			return *c, func() tea.Msg { return CloseCommandDialogMsg{} }
		default:
			if len(msg.Runes) > 0 && msg.Type == tea.KeyRunes {
				c.Query += string(msg.Runes)
				c.filterCommands()
			}
		}
	case ShowCommandDialogMsg:
		c.Show = true
		c.Query = ""
		c.Cursor = 0
		c.Filtered = c.Commands
	case CloseCommandDialogMsg:
		c.Show = false
		c.Query = ""
		c.Cursor = 0
	case CommandSelectedMsg:
		c.Show = false
	}
	return *c, nil
}

func (c *CommandDialog) filterCommands() {
	c.Filtered = nil
	q := strings.ToLower(c.Query)
	for _, cmd := range c.Commands {
		if strings.Contains(strings.ToLower(cmd.Title), q) ||
			strings.Contains(strings.ToLower(cmd.Description), q) {
			c.Filtered = append(c.Filtered, cmd)
		}
	}
	if c.Cursor >= len(c.Filtered) {
		c.Cursor = max(0, len(c.Filtered)-1)
	}
}

func (c CommandDialog) View(totalWidth, totalHeight int) string {
	if !c.Show {
		return ""
	}
	t := theme.CurrentTheme()

	// Render every row at the same width, padded with background color, so
	// cursor moves don't leave trailing characters from the previous row's
	// render. Highlight the active row with the dialog's accent background.
	const rowWidth = 50

	rowStyle := lipgloss.NewStyle().Width(rowWidth).Foreground(t.DialogText())
	cursorStyle := lipgloss.NewStyle().
		Width(rowWidth).
		Foreground(t.DialogBackground()).
		Background(t.DialogAccent()).
		Bold(true)

	// Compute window: leave room for borders, padding, title, and the two
	// "N more" hint lines. Floor at 5 so we always show *something*.
	maxRows := totalHeight - 8
	if maxRows > 15 {
		maxRows = 15
	}
	if maxRows < 5 {
		maxRows = 5
	}

	// Build display strings first, then window them.
	rendered := make([]string, len(c.Filtered))
	for i, cmd := range c.Filtered {
		text := fmt.Sprintf("  %s — %s", cmd.Title, cmd.Description)
		if i == c.Cursor {
			rendered[i] = cursorStyle.Render(text)
		} else {
			rendered[i] = rowStyle.Render(text)
		}
	}
	visible, before, after := Window(rendered, c.Cursor, maxRows)

	hintStyle := lipgloss.NewStyle().Width(rowWidth).Foreground(t.DialogText()).Faint(true)

	var lines []string
	if before > 0 {
		lines = append(lines, hintStyle.Render(fmt.Sprintf("  ↑ %d more", before)))
	}
	lines = append(lines, visible...)
	if after > 0 {
		lines = append(lines, hintStyle.Render(fmt.Sprintf("  ↓ %d more", after)))
	}

	if len(c.Filtered) == 0 {
		muted := lipgloss.NewStyle().
			Width(rowWidth).
			Foreground(t.DialogText()).
			Italic(true).
			Render("  no matching commands")
		lines = []string{muted}
	}
	content := strings.Join(lines, "\n")
	return c.BaseView(content, totalWidth, totalHeight)
}

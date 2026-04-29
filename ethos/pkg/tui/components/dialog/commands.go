package dialog

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
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
	var lines []string
	for i, cmd := range c.Filtered {
		prefix := "  "
		if i == c.Cursor {
			prefix = "> "
		}
		lines = append(lines, fmt.Sprintf("%s%s — %s", prefix, cmd.Title, cmd.Description))
	}
	if len(c.Filtered) == 0 {
		lines = append(lines, "No matching commands")
	}
	content := strings.Join(lines, "\n")
	return c.BaseView(content, totalWidth, totalHeight)
}

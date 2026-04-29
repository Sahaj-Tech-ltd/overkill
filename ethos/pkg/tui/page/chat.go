package page

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Sahaj-Tech-ltd/ethos/pkg/tui/components/chat"

	tuitypes "github.com/Sahaj-Tech-ltd/ethos/pkg/tui/types"
)

type ChatPage struct {
	messages  chat.MessageListModel
	editor    chat.EditorModel
	width     int
	height    int
	agentBusy bool
}

func NewChatPage() ChatPage {
	return ChatPage{
		messages: chat.NewMessageList(),
		editor:   chat.NewEditor(),
	}
}

func (c ChatPage) Init() tea.Cmd {
	return tea.Batch(c.messages.Init(), c.editor.Focus())
}

func (c ChatPage) Update(msg tea.Msg) (ChatPage, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		c.width = msg.Width
		c.height = msg.Height
		msgHeight := msg.Height - 3
		if msgHeight < 1 {
			msgHeight = 1
		}
		c.messages.SetSize(msg.Width, msgHeight)
		c.editor.SetSize(msg.Width, 3)
		return c, nil

	case tuitypes.SendMsg:
		c.agentBusy = true
		if msg.Text != "" {
			c.messages.Append(chat.NewMessage("user", msg.Text))
		}
		return c, nil

	case tuitypes.AgentStreamMsg:
		if msg.Chunk != "" {
			c.messages.Append(chat.NewMessage("assistant", msg.Chunk))
		}
		return c, nil

	case tuitypes.AgentResponseMsg:
		if msg.Err != nil {
			errMsg := fmt.Sprintf("Agent error: %s", msg.Err.Error())
			c.messages.Append(chat.NewMessage("error", errMsg))
			c.agentBusy = false
			return c, nil
		}
		if msg.Done {
			if msg.Content != "" {
				c.messages.Append(chat.NewMessage("assistant", msg.Content))
			}
			c.agentBusy = false
		}
		return c, nil

	case tea.KeyMsg:
		c.messages, cmd = c.messages.Update(msg)
		var editorCmd tea.Cmd
		c.editor, editorCmd = c.editor.Update(msg)
		return c, tea.Batch(cmd, editorCmd)
	}

	c.editor, cmd = c.editor.Update(msg)
	return c, cmd
}

func (c ChatPage) View() string {
	if c.width <= 0 {
		return ""
	}

	sepStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#6c7086"))

	separator := sepStyle.Render(strings.Repeat("─", max(c.width, 1)))

	return lipgloss.JoinVertical(
		lipgloss.Left,
		c.messages.View(),
		separator,
		c.editor.View(),
	)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

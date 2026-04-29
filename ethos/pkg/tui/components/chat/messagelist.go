package chat

import (
	tea "github.com/charmbracelet/bubbletea"
)

type MessageListModel struct {
	messages []Message
	offset   int
	width    int
	height   int
}

func NewMessageList() MessageListModel {
	return MessageListModel{}
}

func (m MessageListModel) Init() tea.Cmd {
	return nil
}

func (m MessageListModel) Len() int {
	return len(m.messages)
}

func (m *MessageListModel) SetSize(w, h int) {
	m.width = w
	m.height = h
}

func (m *MessageListModel) Append(msg Message) {
	m.messages = append(m.messages, msg)
	maxOffset := maxOffset(len(m.messages), m.height)
	m.offset = maxOffset
}

func (m MessageListModel) Update(msg tea.Msg) (MessageListModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up":
			if m.offset > 0 {
				m.offset--
			}
		case "down":
			maxOff := maxOffset(len(m.messages), m.height)
			if m.offset < maxOff {
				m.offset++
			}
		}
	}
	return m, nil
}

func (m MessageListModel) View() string {
	if len(m.messages) == 0 {
		return ""
	}

	maxFit := max(1, m.height)
	maxOff := maxOffset(len(m.messages), maxFit)
	if m.offset > maxOff {
		m.offset = maxOff
	}

	start := m.offset
	end := start + maxFit
	if end > len(m.messages) {
		end = len(m.messages)
	}

	var lines []string
	for i := start; i < end; i++ {
		lines = append(lines, m.messages[i].View(m.width))
	}

	var result string
	for i, line := range lines {
		if i > 0 {
			result += "\n"
		}
		result += line
	}
	return result
}

func maxOffset(totalMessages, height int) int {
	fit := max(1, height)
	if totalMessages <= fit {
		return 0
	}
	return totalMessages - fit
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

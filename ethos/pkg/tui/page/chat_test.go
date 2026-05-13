package page

import (
	"errors"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	tuitypes "github.com/Sahaj-Tech-ltd/overkill/pkg/tui/types"
)

func TestChatPage_Init(t *testing.T) {
	p := NewChatPage(nil)
	cmd := p.Init()
	if cmd == nil {
		t.Error("Init should return cmd")
	}
}

func TestChatPage_UpdateWindowSize(t *testing.T) {
	p := NewChatPage(nil)
	updated, _ := p.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	if updated.width != 100 || updated.height != 40 {
		t.Error("size not set")
	}
}

func TestChatPage_SendMessage(t *testing.T) {
	p := NewChatPage(nil)
	updated, _ := p.Update(tuitypes.SendMsg{Text: "hello"})
	updated, _ = p.Update(tuitypes.AgentStreamMsg{Chunk: "world"})
	updated, _ = p.Update(tuitypes.AgentResponseMsg{Content: "done", Done: true})
	updated, _ = p.Update(tuitypes.AgentResponseMsg{Err: errors.New("fail")})
	if updated.messages.Len() == 0 {
		t.Error("should have error message")
	}
}

func TestChatPage_EditorFocus(t *testing.T) {
	p := NewChatPage(nil)
	_, cmd := p.Update(tea.KeyMsg{Type: tea.KeyRunes})
	_ = cmd
}

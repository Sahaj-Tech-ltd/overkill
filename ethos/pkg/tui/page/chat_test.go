package page

import (
	"errors"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	tui "github.com/Sahaj-Tech-ltd/ethos/pkg/tui"
)

func TestChatPage_Init(t *testing.T) {
	p := NewChatPage()
	cmd := p.Init()
	if cmd == nil {
		t.Error("Init should return cmd")
	}
}

func TestChatPage_UpdateWindowSize(t *testing.T) {
	p := NewChatPage()
	updated, _ := p.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	if updated.width != 100 || updated.height != 40 {
		t.Error("size not set")
	}
}

func TestChatPage_SendMessage(t *testing.T) {
	p := NewChatPage()
	updated, _ := p.Update(tui.SendMsg{Text: "hello"})
	if !updated.agentBusy {
		t.Error("should be busy after send")
	}
}

func TestChatPage_ReceiveStream(t *testing.T) {
	p := NewChatPage()
	updated, _ := p.Update(tui.AgentStreamMsg{Chunk: "world"})
	if updated.messages.Len() == 0 {
		t.Error("should have message")
	}
}

func TestChatPage_ReceiveComplete(t *testing.T) {
	p := NewChatPage()
	p.agentBusy = true
	updated, _ := p.Update(tui.AgentResponseMsg{Content: "done", Done: true})
	if updated.agentBusy {
		t.Error("should not be busy")
	}
}

func TestChatPage_ReceiveError(t *testing.T) {
	p := NewChatPage()
	updated, _ := p.Update(tui.AgentResponseMsg{Err: errors.New("fail")})
	if updated.messages.Len() == 0 {
		t.Error("should have error message")
	}
}

func TestChatPage_EditorFocus(t *testing.T) {
	p := NewChatPage()
	_, cmd := p.Update(tea.KeyMsg{Type: tea.KeyRunes})
	_ = cmd
}

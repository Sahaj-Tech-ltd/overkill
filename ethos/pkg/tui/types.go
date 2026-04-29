package tui

import (
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	tuitypes "github.com/Sahaj-Tech-ltd/ethos/pkg/tui/types"
)

type Focusable interface {
	Focus() tea.Cmd
	Blur() tea.Cmd
	IsFocused() bool
}

type Sizeable interface {
	SetSize(width, height int) tea.Cmd
	GetSize() (int, int)
}

type Bindings interface {
	BindingKeys() []key.Binding
}

type SendMsg = tuitypes.SendMsg
type Attachment = tuitypes.Attachment
type AgentResponseMsg = tuitypes.AgentResponseMsg
type AgentStreamMsg = tuitypes.AgentStreamMsg
type SessionLoadedMsg = tuitypes.SessionLoadedMsg
type CostUpdateMsg = tuitypes.CostUpdateMsg
type FilesChangedMsg = tuitypes.FilesChangedMsg
type FileChange = tuitypes.FileChange
type PersonalityStateMsg = tuitypes.PersonalityStateMsg
type BridgeStatusMsg = tuitypes.BridgeStatusMsg
type SessionListMsg = tuitypes.SessionListMsg
type ModelSelectedMsg = tuitypes.ModelSelectedMsg
type StatusState = tuitypes.StatusState

const (
	StatusIdle      = tuitypes.StatusIdle
	StatusThinking  = tuitypes.StatusThinking
	StatusGenerating = tuitypes.StatusGenerating
	StatusToolCall  = tuitypes.StatusToolCall
)

type BootCompleteMsg struct {
	FunFact string
	SoulMD  string
}

package util

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func CmdHandler(msg tea.Msg) tea.Cmd {
	return func() tea.Msg {
		return msg
	}
}

type InfoType int

const (
	InfoTypeWarn InfoType = iota
	InfoTypeError
	InfoTypeInfo
)

type InfoMsg struct {
	Type InfoType
	Msg  string
	TTL  time.Duration
}

func ReportError(err error) tea.Cmd {
	return CmdHandler(InfoMsg{Type: InfoTypeError, Msg: err.Error()})
}

func ReportWarn(msg string) tea.Cmd {
	return CmdHandler(InfoMsg{Type: InfoTypeWarn, Msg: msg})
}

func ReportInfo(msg string) tea.Cmd {
	return CmdHandler(InfoMsg{Type: InfoTypeInfo, Msg: msg})
}

var SpinnerChars = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

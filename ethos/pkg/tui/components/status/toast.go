package status

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type ToastModel struct {
	message string
	visible bool
	timer   time.Duration
	width   int
}

func NewToastModel() ToastModel {
	return ToastModel{timer: 5 * time.Second}
}

func ShowToast(msg string) tea.Cmd {
	return func() tea.Msg {
		return toastShowMsg{message: msg}
	}
}

type toastShowMsg struct {
	message string
}

type toastHideMsg struct{}

func (t ToastModel) Init() tea.Cmd {
	return nil
}

func (t ToastModel) Update(msg tea.Msg) (ToastModel, tea.Cmd) {
	switch m := msg.(type) {
	case toastShowMsg:
		t.message = m.message
		t.visible = true
		return t, tea.Tick(t.timer, func(time.Time) tea.Msg { return toastHideMsg{} })
	case toastHideMsg:
		t.visible = false
		return t, nil
	}
	return t, nil
}

func (t ToastModel) View() string {
	if !t.visible || t.message == "" {
		return ""
	}
	return lipgloss.NewStyle().
		Background(lipgloss.Color("#45475a")).
		Foreground(lipgloss.Color("#cdd6f4")).
		Padding(0, 2).
		Render(t.message)
}

package chat

import (
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"

	tuitypes "github.com/Sahaj-Tech-ltd/ethos/pkg/tui/types"
)

type EditorModel struct {
	ta      textarea.Model
	focused bool
	width   int
	height  int
}

func NewEditor() EditorModel {
	ta := textarea.New()
	ta.Placeholder = "Type a message..."
	ta.ShowLineNumbers = false
	ta.Prompt = ""
	ta.CharLimit = 0

	return EditorModel{
		ta:      ta,
		focused: false,
		width:   40,
		height:  3,
	}
}

func (e EditorModel) Init() tea.Cmd {
	return nil
}

func (e *EditorModel) Focus() tea.Cmd {
	e.focused = true
	return e.ta.Focus()
}

func (e *EditorModel) Blur() tea.Cmd {
	e.focused = false
	e.ta.Blur()
	return nil
}

func (e EditorModel) IsFocused() bool {
	return e.focused
}

func (e EditorModel) Update(msg tea.Msg) (EditorModel, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "enter" && !msg.Alt {
			val := e.ta.Value()
			if val != "" {
				e.ta.SetValue("")
				return e, func() tea.Msg {
					return tuitypes.SendMsg{Text: val}
				}
			}
		}
	}

	e.ta, cmd = e.ta.Update(msg)
	return e, cmd
}

func (e EditorModel) View() string {
	return e.ta.View()
}

func (e EditorModel) Value() string {
	return e.ta.Value()
}

func (e *EditorModel) SetValue(s string) {
	e.ta.SetValue(s)
}

func (e *EditorModel) SetSize(w, h int) {
	e.width = w
	e.height = h
	e.ta.SetWidth(w)
	e.ta.SetHeight(h)
}

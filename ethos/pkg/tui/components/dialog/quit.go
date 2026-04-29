package dialog

import (
	tea "github.com/charmbracelet/bubbletea"
)

type QuitDialog struct {
	Dialog
}

func NewQuitDialog() QuitDialog {
	return QuitDialog{Dialog: Dialog{Title: "Quit"}}
}

func (q *QuitDialog) Update(msg tea.Msg) (*QuitDialog, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "y":
			return q, tea.Quit
		case "n", "esc":
			q.Show = false
			return q, func() tea.Msg { return CloseQuitMsg{} }
		}
	case ShowQuitMsg:
		q.Show = true
	case CloseQuitMsg:
		q.Show = false
	}
	return q, nil
}

func (q QuitDialog) View(totalWidth, totalHeight int) string {
	if !q.Show {
		return ""
	}
	content := "Quit Ethos?\n\n[y] Yes  [n] No"
	return q.BaseView(content, totalWidth, totalHeight)
}

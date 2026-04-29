package dialog

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
)

type HelpDialog struct {
	Dialog
	Bindings []key.Binding
}

func NewHelpDialog() HelpDialog {
	return HelpDialog{Dialog: Dialog{Title: "Help"}}
}

func (h *HelpDialog) SetBindings(bindings []key.Binding) {
	h.Bindings = bindings
}

func (h *HelpDialog) Update(msg tea.Msg) (*HelpDialog, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.String() == "esc" || msg.String() == "?" {
			h.Show = false
			return h, func() tea.Msg { return CloseHelpMsg{} }
		}
	case ShowHelpMsg:
		h.Show = true
	case CloseHelpMsg:
		h.Show = false
	}
	return h, nil
}

func (h HelpDialog) View(totalWidth, totalHeight int) string {
	if !h.Show {
		return ""
	}
	var lines []string
	for _, b := range h.Bindings {
		lines = append(lines, fmt.Sprintf("%-12s %s", b.Help().Key, b.Help().Desc))
	}
	content := "Key Bindings\n\n" + strings.Join(lines, "\n")
	return h.BaseView(content, totalWidth, totalHeight)
}

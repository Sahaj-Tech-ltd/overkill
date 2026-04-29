package dialog

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/Sahaj-Tech-ltd/ethos/pkg/tui/layout"
	"github.com/Sahaj-Tech-ltd/ethos/pkg/tui/theme"

	tea "github.com/charmbracelet/bubbletea"
)

type Dialog struct {
	Width  int
	Height int
	Show   bool
	Title  string
}

func (d *Dialog) SetSize(w, h int) {
	d.Width = w
	d.Height = h
}

func (d *Dialog) IsShown() bool {
	return d.Show
}

func (d *Dialog) ShowDialog() {
	d.Show = true
}

func (d *Dialog) HideDialog() {
	d.Show = false
}

func (d *Dialog) BaseView(content string, totalWidth, totalHeight int) string {
	t := theme.CurrentTheme()

	borderColor := t.DialogBorder()
	backgroundColor := t.DialogBackground()

	dialogStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Background(backgroundColor).
		Foreground(t.DialogText()).
		Padding(1, 2)

	contentLines := strings.Split(content, "\n")
	maxLineWidth := 0
	for _, line := range contentLines {
		if len(line) > maxLineWidth {
			maxLineWidth = len(line)
		}
	}

	boxWidth := maxLineWidth + 4
	boxHeight := len(contentLines) + 2

	if d.Title != "" {
		dialogStyle = dialogStyle.Bold(true)
		content = d.Title + "\n\n" + content
	}

	rendered := dialogStyle.Render(content)

	if totalWidth <= 0 || totalHeight <= 0 {
		return rendered
	}

	renderedLines := strings.Split(rendered, "\n")
	actualWidth := 0
	for _, line := range renderedLines {
		if lipgloss.Width(line) > actualWidth {
			actualWidth = lipgloss.Width(line)
		}
	}
	actualHeight := len(renderedLines)

	col := (totalWidth - actualWidth) / 2
	row := (totalHeight - actualHeight) / 2
	if col < 0 {
		col = 0
	}
	if row < 0 {
		row = 0
	}

	bg := lipgloss.NewStyle().
		Width(totalWidth).
		Height(totalHeight).
		Background(backgroundColor).
		Render("")

	_ = boxWidth
	_ = boxHeight

	return layout.PlaceOverlay(col, row, rendered, bg, true)
}

func CloseOnEsc(msg tea.Msg) bool {
	if k, ok := msg.(tea.KeyMsg); ok {
		return k.String() == "esc"
	}
	return false
}

func BlockKeys(msg tea.Msg, show bool) bool {
	if !show {
		return false
	}
	_, ok := msg.(tea.KeyMsg)
	return ok
}

type CloseQuitMsg struct{}
type CloseHelpMsg struct{}
type ShowQuitMsg struct{}
type ShowHelpMsg struct{}

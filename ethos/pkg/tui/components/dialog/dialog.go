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

// BaseView returns ONLY the bordered dialog box (no full-screen background
// fill). The parent appModel.View() composites it onto the chat via
// layout.PlaceOverlay. Building a full-screen bg here meant the parent then
// overlaid an opaque bg onto chat, doubling the overlay work and producing
// the rendering glitches users were seeing.
//
// totalWidth/totalHeight are kept in the signature for API compat but only
// used as upper bounds — the dialog never grows beyond a sensible cap.
func (d *Dialog) BaseView(content string, totalWidth, totalHeight int) string {
	_ = strings.TrimSpace // kept for layout import companions
	_ = layout.PlaceOverlay

	t := theme.CurrentTheme()

	dialogStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(t.DialogBorder()).
		Background(t.DialogBackground()).
		Foreground(t.DialogText()).
		Padding(1, 2)

	if d.Title != "" {
		titleLine := lipgloss.NewStyle().
			Foreground(t.DialogAccent()).
			Background(t.DialogBackground()).
			Bold(true).
			Render(d.Title)
		content = titleLine + "\n\n" + content
	}

	// Cap dialog width so it doesn't bleed past terminal edges. Do NOT cap
	// height: lipgloss's MaxHeight silently truncates content (this is what
	// caused only ~3 of 23 slash commands to render). Long dialogs are
	// expected to use Window() for in-dialog scrolling instead.
	if totalWidth > 0 {
		maxW := totalWidth - 4
		if maxW > 0 {
			dialogStyle = dialogStyle.MaxWidth(maxW)
		}
	}

	return dialogStyle.Render(content)
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

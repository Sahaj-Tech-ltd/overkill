package dialog

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Sahaj-Tech-ltd/overkill/pkg/tui/theme"
)

// OverrideChoice is what the user picked in the override-confirm dialog.
type OverrideChoice int

const (
	OverrideCancel OverrideChoice = iota
	OverrideJustSwitch
	OverrideUpdateCreds
)

// OverrideConfirmMsg fires when the user closes the dialog with a choice.
type OverrideConfirmMsg struct {
	Provider string
	Model    string
	Choice   OverrideChoice
}

// CloseOverrideConfirmMsg fires on Esc.
type CloseOverrideConfirmMsg struct{}

// OverrideConfirmDialog asks the user what to do when picking a model whose
// provider is already configured: just switch (keep stored key+endpoint),
// update credentials (open setup wizard pre-filled), or cancel.
type OverrideConfirmDialog struct {
	Dialog
	Provider string
	Model    string
	cursor   int // 0=switch, 1=update, 2=cancel
}

func NewOverrideConfirmDialog() OverrideConfirmDialog {
	return OverrideConfirmDialog{Dialog: Dialog{Title: "Provider already configured"}}
}

// Open seeds the dialog with the model the user just picked and shows it.
func (d *OverrideConfirmDialog) Open(provider, model string) {
	d.Provider = provider
	d.Model = model
	d.cursor = 0
	d.Show = true
}

func (d OverrideConfirmDialog) Update(msg tea.Msg) (OverrideConfirmDialog, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return d, nil
	}
	switch k.String() {
	case "esc":
		d.Show = false
		return d, func() tea.Msg { return CloseOverrideConfirmMsg{} }
	case "up", "k":
		if d.cursor > 0 {
			d.cursor--
		}
	case "down", "j":
		if d.cursor < 2 {
			d.cursor++
		}
	case "s":
		d.Show = false
		return d, d.emit(OverrideJustSwitch)
	case "u":
		d.Show = false
		return d, d.emit(OverrideUpdateCreds)
	case "c":
		d.Show = false
		return d, d.emit(OverrideCancel)
	case "enter":
		choice := []OverrideChoice{OverrideJustSwitch, OverrideUpdateCreds, OverrideCancel}[d.cursor]
		d.Show = false
		return d, d.emit(choice)
	}
	return d, nil
}

func (d OverrideConfirmDialog) emit(c OverrideChoice) tea.Cmd {
	prov, mdl := d.Provider, d.Model
	return func() tea.Msg {
		return OverrideConfirmMsg{Provider: prov, Model: mdl, Choice: c}
	}
}

func (d OverrideConfirmDialog) View(totalWidth, totalHeight int) string {
	if !d.Show {
		return ""
	}
	t := theme.CurrentTheme()
	const rowWidth = 56

	heading := lipgloss.NewStyle().
		Foreground(t.DialogText()).
		Width(rowWidth).
		Render(fmt.Sprintf("  %s is already configured.", d.Provider))
	hint := lipgloss.NewStyle().
		Foreground(t.DialogText()).
		Faint(true).
		Width(rowWidth).
		Render("  switching to " + d.Model)

	rowStyle := lipgloss.NewStyle().Width(rowWidth).Foreground(t.DialogText())
	cursorStyle := lipgloss.NewStyle().
		Width(rowWidth).
		Foreground(t.DialogBackground()).
		Background(t.DialogAccent()).
		Bold(true)

	options := []string{
		"  (s) just switch — keep stored key and endpoint",
		"  (u) update key / endpoint — open the setup wizard",
		"  (c) cancel — keep current model",
	}
	rendered := make([]string, len(options))
	for i, o := range options {
		if i == d.cursor {
			rendered[i] = cursorStyle.Render(o)
		} else {
			rendered[i] = rowStyle.Render(o)
		}
	}

	footer := lipgloss.NewStyle().
		Width(rowWidth).
		Foreground(t.DialogText()).
		Faint(true).
		Render("  ↑↓ enter pick · s/u/c shortcut · esc cancel")

	content := strings.Join([]string{
		heading, hint, "",
		strings.Join(rendered, "\n"),
		"", footer,
	}, "\n")

	return d.BaseView(content, totalWidth, totalHeight)
}

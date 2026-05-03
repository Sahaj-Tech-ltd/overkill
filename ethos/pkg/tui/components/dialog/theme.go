package dialog

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/Sahaj-Tech-ltd/ethos/pkg/tui/theme"
)

type ShowThemeDialogMsg struct{}
type CloseThemeDialogMsg struct{}
type ThemeSelectedMsg struct{ Name string }

type ThemeDialog struct {
	Dialog
	Names    []string
	Cursor   int
	original theme.Theme
}

func NewThemeDialog() ThemeDialog {
	return ThemeDialog{
		Dialog: Dialog{Title: "themes"},
		Names:  theme.Names(),
	}
}

func (d ThemeDialog) Update(msg tea.Msg) (ThemeDialog, tea.Cmd) {
	switch k := msg.(type) {
	case ShowThemeDialogMsg:
		d.Show = true
		d.Cursor = 0
		d.original = theme.CurrentTheme()
		return d, nil
	case CloseThemeDialogMsg:
		d.Show = false
		return d, nil
	case tea.KeyMsg:
		switch k.String() {
		case "up":
			if d.Cursor > 0 {
				d.Cursor--
			} else if len(d.Names) > 0 {
				d.Cursor = len(d.Names) - 1
			}
			d.preview()
		case "down":
			if d.Cursor < len(d.Names)-1 {
				d.Cursor++
			} else {
				d.Cursor = 0
			}
			d.preview()
		case "enter":
			if d.Cursor < len(d.Names) {
				name := d.Names[d.Cursor]
				if t := theme.ByName(name); t != nil {
					theme.SetTheme(t)
				}
				d.Show = false
				return d, func() tea.Msg { return ThemeSelectedMsg{Name: name} }
			}
		case "esc":
			// Revert to the theme active when the dialog opened.
			if d.original != nil {
				theme.SetTheme(d.original)
			}
			d.Show = false
			return d, func() tea.Msg { return CloseThemeDialogMsg{} }
		}
	}
	return d, nil
}

// preview applies the cursor's theme without committing.
func (d ThemeDialog) preview() {
	if d.Cursor < 0 || d.Cursor >= len(d.Names) {
		return
	}
	if t := theme.ByName(d.Names[d.Cursor]); t != nil {
		theme.SetTheme(t)
	}
}

func (d ThemeDialog) View(totalWidth, totalHeight int) string {
	if !d.Show {
		return ""
	}
	t := theme.CurrentTheme()
	var lines []string
	for i, name := range d.Names {
		row := "  " + name
		style := lipgloss.NewStyle().Foreground(t.Text())
		if i == d.Cursor {
			style = style.Foreground(t.Background()).Background(t.Primary()).Bold(true)
			row = "> " + name
		}
		lines = append(lines, style.Render(row))
	}
	hint := lipgloss.NewStyle().Foreground(t.TextMuted()).Render("↑/↓ preview · enter apply · esc revert")
	content := strings.Join(lines, "\n") + "\n\n" + hint
	return d.BaseView(content, totalWidth, totalHeight)
}

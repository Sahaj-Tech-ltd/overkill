package dialog

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Sahaj-Tech-ltd/overkill/internal/plugin"
	"github.com/Sahaj-Tech-ltd/overkill/pkg/tui/theme"
)

// PluginsDialog shows installed plugins, their state, and per-plugin tool/
// command counts. Pressing `t` toggles enable/disable for the highlighted
// plugin (the host persists this back into cfg.Plugins.Disabled).
type PluginsDialog struct {
	Dialog
	statuses []plugin.Status
	cursor   int
}

// PluginToggleMsg is emitted when the user presses `t` on a plugin row.
// The TUI handles persistence and restart.
type PluginToggleMsg struct{ Name string }

// ClosePluginsDialogMsg is emitted on `esc`.
type ClosePluginsDialogMsg struct{}

func NewPluginsDialog() PluginsDialog {
	return PluginsDialog{Dialog: Dialog{Title: "Plugins"}}
}

// SetData refreshes the displayed status snapshot.
func (d *PluginsDialog) SetData(s []plugin.Status) {
	d.statuses = s
	if d.cursor >= len(s) {
		d.cursor = max(0, len(s)-1)
	}
}

func (d PluginsDialog) Update(msg tea.Msg) (PluginsDialog, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "esc", "q":
			d.Show = false
			return d, func() tea.Msg { return ClosePluginsDialogMsg{} }
		case "up":
			if d.cursor > 0 {
				d.cursor--
			}
		case "down":
			if d.cursor < len(d.statuses)-1 {
				d.cursor++
			}
		case "t":
			if d.cursor < len(d.statuses) {
				name := d.statuses[d.cursor].Name
				return d, func() tea.Msg { return PluginToggleMsg{Name: name} }
			}
		}
	}
	return d, nil
}

func (d PluginsDialog) View(w, h int) string {
	if !d.Show {
		return ""
	}
	t := theme.CurrentTheme()
	if len(d.statuses) == 0 {
		return d.BaseView("No plugins installed.\n\nDrop executables or directories with plugin.toml into ~/.overkill/plugins/\n\n[esc] close", w, h)
	}
	var b strings.Builder
	for i, s := range d.statuses {
		dot := lipgloss.NewStyle().Foreground(t.Success()).Render("●")
		state := "running"
		switch {
		case s.Disabled:
			dot = lipgloss.NewStyle().Foreground(t.TextMuted()).Render("○")
			state = "disabled"
		case !s.Running && s.LastError != "":
			dot = lipgloss.NewStyle().Foreground(t.Error()).Render("●")
			state = "error: " + truncate(s.LastError, 50)
		case !s.Running:
			dot = lipgloss.NewStyle().Foreground(t.Warning()).Render("●")
			state = "starting"
		}
		nameStyle := lipgloss.NewStyle().Foreground(t.Text()).Bold(true)
		if i == d.cursor {
			nameStyle = nameStyle.Background(t.DialogAccent()).Foreground(t.DialogBackground())
		}
		line := fmt.Sprintf("%s %s  v%s  %s  · %d tools · %d cmds",
			dot, nameStyle.Render(s.Name), s.Version,
			lipgloss.NewStyle().Foreground(t.TextMuted()).Render(state),
			s.Tools, s.Commands,
		)
		b.WriteString(line)
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(lipgloss.NewStyle().Foreground(t.TextMuted()).Render("[t] toggle  [esc] close"))
	return d.BaseView(b.String(), w, h)
}

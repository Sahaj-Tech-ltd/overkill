package styles

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/Sahaj-Tech-ltd/ethos/pkg/tui/theme"
)

func BaseStyle() lipgloss.Style {
	return lipgloss.NewStyle().Padding(0, 1)
}

func BorderStyle(focused bool) lipgloss.Style {
	borderColor := theme.CurrentTheme().BorderUnfocused()
	if focused {
		borderColor = theme.CurrentTheme().BorderFocused()
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor)
}

func PanelStyle(active bool) lipgloss.Style {
	th := theme.CurrentTheme()
	bg := th.PanelInactive()
	if active {
		bg = th.PanelActive()
	}
	return lipgloss.NewStyle().
		Border(lipgloss.Border{Left: "│", Right: "│"}).
		BorderForeground(th.PanelBorder()).
		Background(bg).
		Padding(0, 1)
}

func RoleLabel(role string) string {
	th := theme.CurrentTheme()
	var color lipgloss.Color
	var label string
	switch role {
	case "user":
		color = th.Primary()
		label = "You:"
	case "assistant":
		color = th.Secondary()
		label = "Ethos:"
	case "tool":
		color = th.TextMuted()
		label = "Tool:"
	case "error":
		color = th.Error()
		label = "Error:"
	default:
		color = th.Text()
		label = role + ":"
	}
	return lipgloss.NewStyle().Foreground(color).Bold(true).Render(label)
}

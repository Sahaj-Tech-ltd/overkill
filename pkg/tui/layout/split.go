package layout

import (
	"github.com/charmbracelet/lipgloss"
)

func HorizontalSplit(left, right string, leftRatio float64, width int) string {
	if width <= 0 {
		return ""
	}
	leftWidth := int(float64(width) * leftRatio)
	if leftWidth < 0 {
		leftWidth = 0
	}
	rightWidth := width - leftWidth
	if rightWidth < 0 {
		rightWidth = 0
	}

	if leftWidth == 0 {
		return lipgloss.NewStyle().Width(rightWidth).Render(right)
	}
	if rightWidth == 0 {
		return lipgloss.NewStyle().Width(leftWidth).Render(left)
	}

	styledLeft := lipgloss.NewStyle().Width(leftWidth).Render(left)
	styledRight := lipgloss.NewStyle().Width(rightWidth).Render(right)
	return lipgloss.JoinHorizontal(lipgloss.Top, styledLeft, styledRight)
}

func VerticalSplit(top, bottom string, height int) string {
	if height <= 0 {
		return ""
	}
	styledTop := lipgloss.NewStyle().Height(height).Render(top)
	return lipgloss.JoinVertical(lipgloss.Left, styledTop, bottom)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

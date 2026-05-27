// Package logo renders the OVERKILL ascii block logo.
package logo

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/Sahaj-Tech-ltd/overkill/deprecated/bubbletea-tui/theme"
)

// 4-row block logo for "OVERKILL", styled after opencode's logo:
// uses █ for filled, ▀ ▄ for half-blocks. 8 letters x 4 rows.
var logoRows = []string{
	"█▀▀█ █  █ █▀▀▀ █▀▀▀ █  █ █ █  █ █",
	"█  █ █  █ █▀▀  █▀▀  █▀▀█ █ █  █ █",
	"█  █  ██  █▀▀▀ █▀▀▀ █  █ █ █▀▀█ █",
	"▀▀▀▀  ▀▀  ▀▀▀▀ ▀▀▀▀ ▀  ▀ ▀ ▀  ▀ ▀",
}

// subtitle rendered below the logo on the home/boot screen.
const Subtitle = "the vibe-coding agent"

// Render returns the OVERKILL logo as a single multi-line string.
// The first half of the logo uses Primary, the trailing portion fades to Accent
// to give a gradient feel similar to opencode's split logo.
func Render(t theme.Theme) string {
	if t == nil {
		t = theme.CurrentTheme()
	}

	primary := lipgloss.NewStyle().Foreground(t.Primary()).Bold(true)
	accent := lipgloss.NewStyle().Foreground(t.Accent()).Bold(true)

	out := make([]string, 0, len(logoRows))
	for _, row := range logoRows {
		// Split the row roughly in half so left = primary, right = accent.
		runes := []rune(row)
		mid := len(runes) / 2
		// Walk forward to a space so we don't split a glyph mid-letter.
		for mid < len(runes) && runes[mid] != ' ' {
			mid++
		}
		left := primary.Render(string(runes[:mid]))
		right := accent.Render(string(runes[mid:]))
		out = append(out, left+right)
	}
	return strings.Join(out, "\n")
}

// Width returns the rendered width of the logo (without ANSI codes).
func Width() int {
	return lipgloss.Width(logoRows[0])
}

// Height returns the number of logo rows.
func Height() int {
	return len(logoRows)
}

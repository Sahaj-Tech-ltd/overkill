package styles

import (
	"github.com/charmbracelet/glamour"

	"github.com/Sahaj-Tech-ltd/ethos/pkg/tui/theme"
)

// RenderMarkdown converts markdown into ANSI styled output sized to width.
//
// We pre-process markdown table blocks ourselves before handing the content
// to Glamour. Glamour's table renderer ignores alignment hints and wraps
// badly in narrow terminals; our renderer respects `:---:` and budgets
// column widths against the available terminal width.
func RenderMarkdown(content string, width int) string {
	if width > 0 {
		content = PreprocessTables(content, width-4, theme.CurrentTheme())
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return content
	}
	out, err := r.Render(content)
	if err != nil {
		return content
	}
	return out
}

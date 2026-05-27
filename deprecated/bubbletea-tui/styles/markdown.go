package styles

import (
	"sync/atomic"

	"github.com/charmbracelet/glamour"

	"github.com/Sahaj-Tech-ltd/overkill/deprecated/bubbletea-tui/theme"
)

// concealMarkdown, when true, makes RenderMarkdown return the raw text
// instead of styled ANSI. Used by the /conceal toggle so the user can grab
// raw markdown for copy-paste without ANSI artifacts.
var concealMarkdown atomic.Bool

// SetConcealMarkdown flips the conceal toggle. Returns the previous value.
func SetConcealMarkdown(on bool) bool {
	return concealMarkdown.Swap(on)
}

// IsConcealMarkdown reports the current state.
func IsConcealMarkdown() bool { return concealMarkdown.Load() }

// RenderMarkdown converts markdown into ANSI styled output sized to width.
//
// We pre-process markdown table blocks ourselves before handing the content
// to Glamour. Glamour's table renderer ignores alignment hints and wraps
// badly in narrow terminals; our renderer respects `:---:` and budgets
// column widths against the available terminal width.
func RenderMarkdown(content string, width int) string {
	if concealMarkdown.Load() {
		// Conceal mode: hand back the raw text exactly as the model produced
		// it so the user can copy-paste without ANSI escapes.
		return content
	}
	if width > 0 {
		content = PreprocessTables(content, width-4, theme.CurrentTheme())
	}
	// Line-number gutter for long code blocks. Off in conceal mode so
	// copy-pasted code doesn't carry "N│ " prefixes (handled above).
	content = addCodeBlockGutters(content)
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

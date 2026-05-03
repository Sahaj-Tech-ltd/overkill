package layout

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// PlaceOverlay composes `overlay` onto `background` at visual column `col`
// and row `row`. CRITICAL: slicing must use visual-width-aware helpers from
// x/ansi, not byte indices — when bgLine contains ANSI escape sequences
// (which any styled lipgloss line does), byte slicing cuts mid-escape and
// leaves the terminal in undefined color state. That manifests as "next
// option breaks the UI" / "terminal theme bleeds through" bugs.
//
// `clear` is kept for API compatibility; both modes use the same slicing
// path now since byte vs visual indexing was the only real difference.
func PlaceOverlay(col, row int, overlay, background string, clear bool) string {
	_ = clear
	bgLines := strings.Split(background, "\n")
	overlayLines := strings.Split(overlay, "\n")

	if len(bgLines) == 0 {
		return overlay
	}

	bgWidth := 0
	for _, line := range bgLines {
		if w := ansi.StringWidth(line); w > bgWidth {
			bgWidth = w
		}
	}

	result := make([]string, len(bgLines))
	for i, bgLine := range bgLines {
		if i < row || i >= row+len(overlayLines) {
			result[i] = bgLine
			continue
		}

		overlayLine := overlayLines[i-row]
		overlayWidth := ansi.StringWidth(overlayLine)

		// ansi.Cut(s, left, right) returns the substring spanning visual
		// columns [left, right). Pad the background out so Cut has stable
		// boundaries even if the bg line is shorter than where the overlay
		// is being placed.
		padded := bgLine
		bgLineWidth := ansi.StringWidth(bgLine)
		need := col + overlayWidth - bgLineWidth
		if need > 0 {
			padded = bgLine + strings.Repeat(" ", need)
		}

		prefix := ansi.Cut(padded, 0, col)
		end := bgWidth
		if col+overlayWidth > end {
			end = col + overlayWidth
		}
		suffix := ansi.Cut(padded, col+overlayWidth, end)

		result[i] = prefix + overlayLine + suffix
	}

	return strings.Join(result, "\n")
}

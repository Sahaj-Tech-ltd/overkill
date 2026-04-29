package layout

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

func PlaceOverlay(col, row int, overlay, background string, clear bool) string {
	bgLines := strings.Split(background, "\n")
	overlayLines := strings.Split(overlay, "\n")

	bgHeight := len(bgLines)
	if bgHeight == 0 {
		return overlay
	}

	bgWidth := 0
	for _, line := range bgLines {
		w := ansi.StringWidth(line)
		if w > bgWidth {
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
		bgLineWidth := ansi.StringWidth(bgLine)

		var newLine string
		if clear {
			padded := bgLine
			padLen := maxInt(0, col+overlayWidth-bgLineWidth)
			if padLen > 0 {
				padded = padded + strings.Repeat(" ", padLen)
			}
			newLine = padded[:col] + overlayLine
			remainder := col + overlayWidth
			if remainder < len(padded) {
				newLine += padded[remainder:]
			}
		} else {
			padded := bgLine
			padLen := maxInt(0, col+overlayWidth-bgLineWidth)
			if padLen > 0 {
				padded = padded + strings.Repeat(" ", padLen)
			}
			prefix := padded[:col]
			suffix := ""
			if col+overlayWidth < len(padded) {
				suffix = padded[col+overlayWidth:]
			}
			newLine = prefix + overlayLine + suffix
		}

		result[i] = newLine
	}

	return strings.Join(result, "\n")
}

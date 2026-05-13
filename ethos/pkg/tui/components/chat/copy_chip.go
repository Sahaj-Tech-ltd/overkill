// Package chat — code-block extraction + copy-chip footer rendering.
//
// Each non-streaming assistant message that contains fenced code blocks
// gets a footer row of clickable chips: "▸ 1   ▸ 2   ▸ 3". The renderer
// emits the footer; MessageList computes the absolute screen position
// of each chip and registers a CopyZone so the mouse handler can map
// (X, Y) → code-block body.
package chat

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/Sahaj-Tech-ltd/overkill/pkg/tui/theme"
)

// copyChipLeftPad is the indent before the first chip on the footer
// row. Matches the body indent of an assistant message so chips line
// up under the text.
const copyChipLeftPad = 2

// copyChipGap is the visual gap between adjacent chips.
const copyChipGap = 2

// fencedCodeWithLang captures (lang, body) for backtick and tilde
// fences. Lang character set covers the common forms (alphanumerics,
// underscores, dashes); exotic tags like "c++" fall through to a bare-
// fence match with empty lang.
var fencedCodeWithLang = regexp.MustCompile("(?s)(?:```|~~~)([a-zA-Z0-9_-]*)\\s*\\n(.*?)\\n(?:```|~~~)")

// CodeBlock is one fenced block extracted from a message body.
type CodeBlock struct {
	Lang string
	Body string
}

// ExtractCodeBlocks pulls fenced code blocks from s in source order.
// Exposed for tests + for the message renderer.
func ExtractCodeBlocks(s string) []CodeBlock {
	matches := fencedCodeWithLang.FindAllStringSubmatch(s, -1)
	out := make([]CodeBlock, 0, len(matches))
	for _, m := range matches {
		out = append(out, CodeBlock{
			Lang: strings.TrimSpace(m[1]),
			Body: m[2],
		})
	}
	return out
}

// chipLayout records the column range (left-relative) of one chip on
// the footer row. Used by MessageList to register CopyZones with the
// global registry. ColEnd is exclusive — matches the half-open convention
// of CopyZone.MaxX.
type chipLayout struct {
	ColStart int
	ColEnd   int
}

// buildCopyFooter assembles the footer line and the layout for each
// chip. The hovered argument lets the renderer highlight the chip the
// mouse is currently over; pass -1 to render every chip in the resting
// style.
//
// We render each chip with lipgloss styling and accumulate widths via
// lipgloss.Width so RTL / wide-char content doesn't throw the layout
// off (lipgloss reports cell width, not byte length).
func buildCopyFooter(t theme.Theme, blocks []CodeBlock, hovered int) (string, []chipLayout) {
	if len(blocks) == 0 {
		return "", nil
	}
	resting := lipgloss.NewStyle().
		Foreground(t.TextMuted()).
		Padding(0, 1)
	active := lipgloss.NewStyle().
		Foreground(t.Background()).
		Background(t.Accent()).
		Bold(true).
		Padding(0, 1)

	var (
		parts   []string
		layouts []chipLayout
		col     = copyChipLeftPad
	)
	for i := range blocks {
		text := fmt.Sprintf("▸ %d", i+1)
		var chip string
		if i == hovered {
			chip = active.Render(text)
		} else {
			chip = resting.Render(text)
		}
		w := lipgloss.Width(chip)
		layouts = append(layouts, chipLayout{ColStart: col, ColEnd: col + w})
		parts = append(parts, chip)
		col += w + copyChipGap
	}
	// Lead the line with the indent. lipgloss is fine joining
	// styled strings directly with spaces.
	footer := strings.Repeat(" ", copyChipLeftPad) + strings.Join(parts, strings.Repeat(" ", copyChipGap))
	return footer, layouts
}

// CopyChipLayout is the externally-visible shape returned by
// Message.CopyChips. Identical to chipLayout but with the body+lang
// attached so MessageList can register zones without re-extracting
// the code blocks.
type CopyChipLayout struct {
	ColStart int
	ColEnd   int
	Body     string
	Lang     string
}

// CopyChips returns the column layout + payload for each clickable
// chip on this message's footer row. Returns nil when the message
// isn't assistant-role, is streaming, or has no code blocks.
//
// The hovered argument MUST match the value that was passed to
// renderAssistant during the most recent View — otherwise the widths
// returned here won't agree with what's on screen (the hovered chip
// has padding from the active style; resting chips have less). The
// caller (MessageList) knows the current hover from the package
// registry, so this stays consistent.
func (m Message) CopyChips(width int, hovered int) []CopyChipLayout {
	if m.Role != "assistant" || m.Streaming {
		return nil
	}
	blocks := ExtractCodeBlocks(m.Content)
	if len(blocks) == 0 {
		return nil
	}
	_, layouts := buildCopyFooter(theme.CurrentTheme(), blocks, hovered)
	out := make([]CopyChipLayout, len(layouts))
	for i, l := range layouts {
		out[i] = CopyChipLayout{
			ColStart: l.ColStart,
			ColEnd:   l.ColEnd,
			Body:     blocks[i].Body,
			Lang:     blocks[i].Lang,
		}
	}
	return out
}

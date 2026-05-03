// Package styles — terminal-width-aware markdown table renderer.
//
// Glamour's built-in table renderer ignores alignment hints and wraps badly
// in narrow terminals. We pre-process raw markdown, find table blocks, render
// them ourselves into properly aligned ANSI, and splice the result back into
// the markdown content before handing it to Glamour. Glamour passes ANSI
// through unchanged so the spliced segments survive the second render pass.
package styles

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"

	"github.com/Sahaj-Tech-ltd/ethos/pkg/tui/theme"
)

// Alignment for a single column. The markdown alignment row encodes these
// with `:---` (left), `:---:` (center), `---:` (right).
type Alignment int

const (
	AlignLeft Alignment = iota
	AlignCenter
	AlignRight
)

// minColWidth keeps narrow columns legible — a column is never squeezed
// below this many characters of body content.
const minColWidth = 3

// RenderTable formats rows into an ANSI table that fits within maxWidth.
// rows[0] is the header. alignments must have len == len(rows[0]); pass
// nil to default every column to AlignLeft.
//
// The widest column is greedily truncated with `…` when total content
// exceeds the budget, so even very wide tables fit the chat width.
func RenderTable(rows [][]string, alignments []Alignment, maxWidth int, t theme.Theme) string {
	if len(rows) == 0 || len(rows[0]) == 0 {
		return ""
	}
	cols := len(rows[0])
	if alignments == nil {
		alignments = make([]Alignment, cols)
	}
	// Normalize: every row gets exactly `cols` cells.
	for i := range rows {
		for len(rows[i]) < cols {
			rows[i] = append(rows[i], "")
		}
		if len(rows[i]) > cols {
			rows[i] = rows[i][:cols]
		}
	}

	widths := computeWidths(rows, maxWidth, cols)

	headerStyle := lipgloss.NewStyle().Foreground(t.Accent()).Bold(true)
	bodyStyle := lipgloss.NewStyle().Foreground(t.Text())
	sepStyle := lipgloss.NewStyle().Foreground(t.Border())

	gutter := sepStyle.Render("│")

	var b strings.Builder

	// Header row
	headerCells := make([]string, cols)
	for c, cell := range rows[0] {
		headerCells[c] = headerStyle.Render(padCell(cell, widths[c], alignments[c]))
	}
	b.WriteString(joinCells(headerCells, gutter))
	b.WriteString("\n")

	// Separator row (drawn as ─── per column)
	sepCells := make([]string, cols)
	for c := range cols {
		sepCells[c] = sepStyle.Render(strings.Repeat("─", widths[c]+2))
	}
	b.WriteString(strings.Join(sepCells, sepStyle.Render("┼")))
	b.WriteString("\n")

	// Body rows
	for r := 1; r < len(rows); r++ {
		cells := make([]string, cols)
		for c, cell := range rows[r] {
			cells[c] = bodyStyle.Render(padCell(cell, widths[c], alignments[c]))
		}
		b.WriteString(joinCells(cells, gutter))
		if r < len(rows)-1 {
			b.WriteString("\n")
		}
	}

	return b.String()
}

func joinCells(cells []string, gutter string) string {
	// Each cell already has 1-space padding either side (see padCell).
	parts := make([]string, len(cells))
	for i, c := range cells {
		parts[i] = " " + c + " "
	}
	return strings.Join(parts, gutter)
}

// padCell truncates and pads a cell's content to the target visible width.
// Truncation appends `…` so the user knows content was lost.
func padCell(s string, width int, align Alignment) string {
	if width <= 0 {
		return ""
	}
	rs := []rune(s)
	if len(rs) > width {
		if width <= 1 {
			return "…"
		}
		s = string(rs[:width-1]) + "…"
	}
	curr := utf8.RuneCountInString(s)
	pad := width - curr
	if pad <= 0 {
		return s
	}
	switch align {
	case AlignRight:
		return strings.Repeat(" ", pad) + s
	case AlignCenter:
		left := pad / 2
		right := pad - left
		return strings.Repeat(" ", left) + s + strings.Repeat(" ", right)
	default:
		return s + strings.Repeat(" ", pad)
	}
}

// computeWidths picks a width per column that fits the maxWidth budget.
// Each column needs +3 chrome (gutter + 2 spaces of padding); we account
// for that here, then greedily shrink the widest column when over budget.
func computeWidths(rows [][]string, maxWidth, cols int) []int {
	widths := make([]int, cols)
	for _, row := range rows {
		for c, cell := range row {
			if c >= cols {
				break
			}
			n := utf8.RuneCountInString(cell)
			if n > widths[c] {
				widths[c] = n
			}
		}
	}
	// Floor every column at minColWidth so single-letter cells still read.
	for c := range widths {
		if widths[c] < minColWidth {
			widths[c] = minColWidth
		}
	}
	if maxWidth <= 0 {
		return widths
	}
	// Chrome: each column contributes 2 spaces of padding plus 1 gutter
	// between every neighbouring pair.
	chrome := cols*2 + (cols - 1)
	budget := maxWidth - chrome
	if budget < cols*minColWidth {
		budget = cols * minColWidth
	}
	// Shrink widest column repeatedly until we fit.
	for sum(widths) > budget {
		widest := 0
		for c, w := range widths {
			if w > widths[widest] {
				widest = c
			}
		}
		if widths[widest] <= minColWidth {
			break
		}
		widths[widest]--
	}
	return widths
}

func sum(xs []int) int {
	s := 0
	for _, x := range xs {
		s += x
	}
	return s
}

// tableBlockRE matches a contiguous markdown table: header row, alignment
// row, and one or more body rows. The (?m) flag makes ^ and $ match per-line.
var tableBlockRE = regexp.MustCompile(`(?m)^\|[^\n]*\|[ \t]*\n\|[\s|:\-]+\|[ \t]*\n(?:\|[^\n]*\|[ \t]*\n?)+`)

// PreprocessTables finds every markdown table block in `content`, renders each
// to ANSI via RenderTable, and substitutes the rendered output back inline.
// Glamour preserves ANSI escapes, so the substituted output survives the
// second render pass.
func PreprocessTables(content string, maxWidth int, t theme.Theme) string {
	return tableBlockRE.ReplaceAllStringFunc(content, func(block string) string {
		rows, aligns, ok := parseTableBlock(block)
		if !ok {
			return block
		}
		rendered := RenderTable(rows, aligns, maxWidth, t)
		// Surround with blank lines so Glamour treats it as a standalone
		// paragraph — otherwise it may try to wrap it into surrounding text.
		return "\n" + rendered + "\n"
	})
}

// parseTableBlock parses the regex match into rows + alignments. Returns
// ok=false when the block doesn't actually have a valid alignment row.
func parseTableBlock(block string) ([][]string, []Alignment, bool) {
	lines := strings.Split(strings.TrimRight(block, "\n"), "\n")
	if len(lines) < 2 {
		return nil, nil, false
	}
	header := splitRow(lines[0])
	alignRow := splitRow(lines[1])
	if len(alignRow) == 0 {
		return nil, nil, false
	}
	aligns := make([]Alignment, len(header))
	for i := range aligns {
		if i >= len(alignRow) {
			aligns[i] = AlignLeft
			continue
		}
		aligns[i] = parseAlignment(alignRow[i])
	}
	rows := [][]string{header}
	for _, l := range lines[2:] {
		rows = append(rows, splitRow(l))
	}
	return rows, aligns, true
}

// splitRow splits a `| a | b |` row into trimmed cells.
func splitRow(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "|")
	line = strings.TrimSuffix(line, "|")
	parts := strings.Split(line, "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

// parseAlignment reads `:---:` / `---:` / `:---` / `---` into an Alignment.
func parseAlignment(s string) Alignment {
	s = strings.TrimSpace(s)
	left := strings.HasPrefix(s, ":")
	right := strings.HasSuffix(s, ":")
	switch {
	case left && right:
		return AlignCenter
	case right:
		return AlignRight
	default:
		return AlignLeft
	}
}

// Package dialog — standalone diff viewer overlay.
//
// Renders a unified diff with semantic color (added green, removed red,
// context muted). `s` toggles a side-by-side split view that pairs deletions
// with additions in two columns. Reused by the patch permission preview and
// the `/diff` slash command.
package dialog

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	diffpkg "github.com/Sahaj-Tech-ltd/ethos/internal/diff"
	"github.com/Sahaj-Tech-ltd/ethos/pkg/tui/theme"
)

// CloseDiffDialogMsg is emitted on Esc.
type CloseDiffDialogMsg struct{}

// DiffDialog renders a unified diff.
type DiffDialog struct {
	Dialog
	Path      string
	Diff      string
	Scroll    int  // top line offset
	SplitMode bool // false = unified, true = side-by-side
}

// NewDiffDialog returns a fresh, hidden dialog.
func NewDiffDialog() DiffDialog {
	return DiffDialog{Dialog: Dialog{Title: "diff"}}
}

// SetDiff configures the body.
func (d *DiffDialog) SetDiff(path, diff string) {
	d.Path = path
	d.Diff = diff
	d.Scroll = 0
}

// SetSplitMode is provided for callers (and tests) that want to seed the
// dialog directly into split mode.
func (d *DiffDialog) SetSplitMode(on bool) {
	d.SplitMode = on
	d.Scroll = 0
}

// Update handles scroll, split toggle, and dismiss.
func (d DiffDialog) Update(msg tea.Msg) (DiffDialog, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return d, nil
	}
	switch k.String() {
	case "up", "k":
		if d.Scroll > 0 {
			d.Scroll--
		}
	case "down", "j":
		d.Scroll++
	case "s":
		d.SplitMode = !d.SplitMode
		d.Scroll = 0
	case "esc", "enter", "q":
		d.Show = false
		return d, func() tea.Msg { return CloseDiffDialogMsg{} }
	}
	return d, nil
}

// View renders the diff.
func (d DiffDialog) View(totalWidth, totalHeight int) string {
	if !d.Show {
		return ""
	}
	var body string
	if d.SplitMode {
		// Account for dialog chrome (border + padding) when sizing the
		// columns. BaseView caps at totalWidth-4 and pads internally.
		colsWidth := totalWidth - 8
		if colsWidth < 30 {
			colsWidth = 30
		}
		body = RenderDiffSplit(d.Path, d.Diff, colsWidth)
	} else {
		body = RenderDiffBody(d.Path, d.Diff)
	}
	if d.Scroll > 0 {
		lines := strings.Split(body, "\n")
		if d.Scroll < len(lines) {
			body = strings.Join(lines[d.Scroll:], "\n")
		}
	}
	hint := lipgloss.NewStyle().
		Foreground(theme.CurrentTheme().TextMuted()).
		Render(splitHint(d.SplitMode))
	body += "\n\n" + hint
	return d.BaseView(body, totalWidth, totalHeight)
}

func splitHint(splitMode bool) string {
	if splitMode {
		return "[s] unified  ·  [esc] close"
	}
	return "[s] side-by-side  ·  [esc] close"
}

// RenderDiffBody applies semantic color to a unified diff string.
// Exposed so the permission dialog can reuse it.
func RenderDiffBody(path, diff string) string {
	t := theme.CurrentTheme()
	addStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#22c55e"))
	delStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#ef4444"))
	hdrStyle := lipgloss.NewStyle().Foreground(t.Accent()).Bold(true)
	ctxStyle := lipgloss.NewStyle().Foreground(t.TextMuted())

	var b strings.Builder
	if path != "" {
		b.WriteString(hdrStyle.Render("--- " + path))
		b.WriteString("\n")
	}
	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "@@"):
			b.WriteString(hdrStyle.Render(line))
		case strings.HasPrefix(line, "+++ ") || strings.HasPrefix(line, "--- "):
			b.WriteString(hdrStyle.Render(line))
		case strings.HasPrefix(line, "+"):
			b.WriteString(addStyle.Render(line))
		case strings.HasPrefix(line, "-"):
			b.WriteString(delStyle.Render(line))
		default:
			b.WriteString(ctxStyle.Render(line))
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// RenderDiffSplit renders a unified diff as two columns. totalColsWidth is
// the width budget for both columns plus the 1-cell gutter; each column
// receives (totalColsWidth - 3) / 2.
func RenderDiffSplit(path, diff string, totalColsWidth int) string {
	t := theme.CurrentTheme()
	hdrStyle := lipgloss.NewStyle().Foreground(t.Accent()).Bold(true)
	mutedStyle := lipgloss.NewStyle().Foreground(t.TextMuted())
	delBg := lipgloss.NewStyle().Background(lipgloss.Color("#3b1212")).Foreground(lipgloss.Color("#fca5a5"))
	addBg := lipgloss.NewStyle().Background(lipgloss.Color("#0f3a1f")).Foreground(lipgloss.Color("#86efac"))
	ctxStyle := lipgloss.NewStyle().Foreground(t.Text())

	colWidth := (totalColsWidth - 3) / 2
	if colWidth < 12 {
		colWidth = 12
	}
	const lineNumWidth = 4
	contentWidth := colWidth - lineNumWidth - 1
	if contentWidth < 5 {
		contentWidth = 5
	}

	gutter := mutedStyle.Render("│")

	var b strings.Builder
	if path != "" {
		b.WriteString(hdrStyle.Render("--- " + path))
		b.WriteString("\n")
	}

	hunks := diffpkg.ParseHunks(diff)
	for hi, h := range hunks {
		if hi > 0 {
			b.WriteString(mutedStyle.Render(strings.Repeat("═", totalColsWidth)))
			b.WriteString("\n")
		}
		hdrLine := fmt.Sprintf("@@ -%d  +%d @@", h.LeftStart, h.RightStart)
		b.WriteString(hdrStyle.Render(hdrLine))
		b.WriteString("\n")
		for _, row := range diffpkg.Pair(h) {
			leftCell := renderSplitCell(row.Left, row.LeftNum, row.LeftDel, contentWidth, lineNumWidth, ctxStyle, mutedStyle, delBg)
			rightCell := renderSplitCell(row.Right, row.RightNum, row.RightAdd, contentWidth, lineNumWidth, ctxStyle, mutedStyle, addBg)
			b.WriteString(leftCell)
			b.WriteString(gutter)
			b.WriteString(rightCell)
			b.WriteString("\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// renderSplitCell formats a single side of a LineRow. nil content renders an
// empty pad so column alignment stays intact.
func renderSplitCell(s *string, num int, highlight bool, contentWidth, numWidth int, ctxStyle, numStyle, hlStyle lipgloss.Style) string {
	numCol := strings.Repeat(" ", numWidth)
	body := strings.Repeat(" ", contentWidth)
	if s != nil {
		numCol = numStyle.Render(padRight(fmt.Sprintf("%d", num), numWidth))
		content := *s
		if len([]rune(content)) > contentWidth {
			content = string([]rune(content)[:contentWidth-1]) + "…"
		}
		text := padRight(content, contentWidth)
		if highlight {
			body = hlStyle.Render(text)
		} else {
			body = ctxStyle.Render(text)
		}
	} else {
		// Keep number column padded so columns align.
		numCol = numStyle.Render(numCol)
		body = ctxStyle.Render(body)
	}
	return numCol + " " + body
}

func padRight(s string, w int) string {
	r := []rune(s)
	if len(r) >= w {
		return string(r[:w])
	}
	return s + strings.Repeat(" ", w-len(r))
}

// Package dialog — full subagent detail overlay.
package dialog

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Sahaj-Tech-ltd/ethos/internal/subagent"
	"github.com/Sahaj-Tech-ltd/ethos/pkg/tui/theme"
)

// CloseSubagentFullDialogMsg fires on Esc.
type CloseSubagentFullDialogMsg struct{}

// SubagentFullDialog shows the detail view for one running or completed
// subagent — goal, status, elapsed time, tool trace, costs.
type SubagentFullDialog struct {
	Dialog
	children []subagent.ChildRef
	cursor   int
	scroll   int
}

func NewSubagentFullDialog() SubagentFullDialog {
	return SubagentFullDialog{Dialog: Dialog{Title: "subagents"}}
}

// SetChildren installs the subagent list.
func (d *SubagentFullDialog) SetChildren(cs []subagent.ChildRef) {
	d.children = append([]subagent.ChildRef(nil), cs...)
	if d.cursor >= len(d.children) {
		d.cursor = 0
	}
}

// SetCursor moves focus to the given child index (used when entering from
// the footer keystroke).
func (d *SubagentFullDialog) SetCursor(i int) {
	if i >= 0 && i < len(d.children) {
		d.cursor = i
	}
}

func (d SubagentFullDialog) Update(msg tea.Msg) (SubagentFullDialog, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return d, nil
	}
	switch k.String() {
	case "up", "k":
		if d.cursor > 0 {
			d.cursor--
			d.scroll = 0
		}
	case "down", "j":
		if d.cursor < len(d.children)-1 {
			d.cursor++
			d.scroll = 0
		}
	case "esc", "q":
		d.Show = false
		return d, func() tea.Msg { return CloseSubagentFullDialogMsg{} }
	}
	return d, nil
}

func (d SubagentFullDialog) View(w, h int) string {
	if !d.Show {
		return ""
	}
	t := theme.CurrentTheme()
	hi := lipgloss.NewStyle().Foreground(t.Accent()).Bold(true)
	muted := lipgloss.NewStyle().Foreground(t.TextMuted())
	if len(d.children) == 0 {
		return d.BaseView("(no subagents)", w, h)
	}

	var b strings.Builder
	// List (windowed for long subagent counts)
	rows := make([]string, len(d.children))
	for i, c := range d.children {
		marker := "  "
		if i == d.cursor {
			marker = hi.Render("▸ ")
		}
		goal := c.Goal
		if len(goal) > 28 {
			goal = goal[:25] + "..."
		}
		rows[i] = fmt.Sprintf("%s%s · %s", marker, goal, c.Status)
	}
	// Reserve more chrome here than other dialogs because the detail block
	// below needs vertical space too.
	listMax := WindowSize(h) - 6
	if listMax < 3 {
		listMax = 3
	}
	visible, before, after := Window(rows, d.cursor, listMax)
	if before > 0 {
		b.WriteString(muted.Render(fmt.Sprintf("  ↑ %d more\n", before)))
	}
	for _, line := range visible {
		b.WriteString(line)
		b.WriteString("\n")
	}
	if after > 0 {
		b.WriteString(muted.Render(fmt.Sprintf("  ↓ %d more\n", after)))
	}
	b.WriteString("\n")

	// Detail
	c := d.children[d.cursor]
	elapsed := time.Since(c.StartedAt).Round(time.Second)
	b.WriteString(hi.Render("[ "+c.Goal+" ]") + "\n")
	b.WriteString(fmt.Sprintf("status:    %s\n", c.Status))
	b.WriteString(fmt.Sprintf("model:     %s\n", c.Model))
	b.WriteString(fmt.Sprintf("started:   %s\n", c.StartedAt.Local().Format("15:04:05")))
	b.WriteString(fmt.Sprintf("elapsed:   %s\n", elapsed))
	if c.Result != nil {
		r := c.Result
		b.WriteString(fmt.Sprintf("tokens:    in=%d out=%d\n", r.TokensIn, r.TokensOut))
		b.WriteString(fmt.Sprintf("cost:      $%.4f\n", r.CostUSD))
		if r.Summary != "" {
			summary := r.Summary
			if len(summary) > 200 {
				summary = summary[:197] + "..."
			}
			b.WriteString("\nsummary:\n  " + summary + "\n")
		}
		if len(r.ToolTrace) > 0 {
			b.WriteString("\ntool calls:\n")
			for _, te := range r.ToolTrace {
				b.WriteString(fmt.Sprintf("  · %s (args=%dB)\n", te.Tool, te.ArgsLen))
			}
		}
		if len(r.FilesRead) > 0 || len(r.FilesWritten) > 0 {
			b.WriteString(muted.Render(fmt.Sprintf("\nfiles read=%d  written=%d\n",
				len(r.FilesRead), len(r.FilesWritten))))
		}
	}
	b.WriteString(muted.Render("\nj/k: select  ·  esc: close"))
	return d.BaseView(strings.TrimRight(b.String(), "\n"), w, h)
}

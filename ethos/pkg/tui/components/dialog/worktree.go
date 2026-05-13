package dialog

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Sahaj-Tech-ltd/overkill/internal/worktree"
	"github.com/Sahaj-Tech-ltd/overkill/pkg/tui/theme"
)

// WorktreeDialog lists git worktrees with status and an "add" affordance.
type WorktreeDialog struct {
	Dialog
	entries []worktree.Worktree
	cursor  int
	hint    string
}

type CloseWorktreeDialogMsg struct{}

// WorktreeAddRequestMsg is emitted when the user presses 'a' to request
// creating a new worktree. The TUI handles this by toasting usage info or
// opening an input affordance.
type WorktreeAddRequestMsg struct{}

func NewWorktreeDialog() WorktreeDialog {
	return WorktreeDialog{Dialog: Dialog{Title: "Worktrees"}}
}

func (d *WorktreeDialog) SetEntries(entries []worktree.Worktree) {
	d.entries = entries
	if d.cursor >= len(entries) {
		d.cursor = 0
	}
}

func (d *WorktreeDialog) SetHint(s string) { d.hint = s }

func (d WorktreeDialog) Update(msg tea.Msg) (WorktreeDialog, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "esc", "q":
			d.Show = false
			return d, func() tea.Msg { return CloseWorktreeDialogMsg{} }
		case "j", "down":
			if d.cursor < len(d.entries)-1 {
				d.cursor++
			}
		case "k", "up":
			if d.cursor > 0 {
				d.cursor--
			}
		case "a":
			return d, func() tea.Msg { return WorktreeAddRequestMsg{} }
		}
	}
	return d, nil
}

func (d WorktreeDialog) View(w, h int) string {
	if !d.Show {
		return ""
	}
	t := theme.CurrentTheme()
	if len(d.entries) == 0 {
		body := "No worktrees yet.\n\nUsage: type `/worktree add <path> <branch>` to create one.\n\n[a] add  [esc] close"
		if d.hint != "" {
			body = d.hint + "\n\n" + body
		}
		return d.BaseView(body, w, h)
	}
	muted := lipgloss.NewStyle().Foreground(t.TextMuted())
	rendered := make([]string, len(d.entries))
	for i, wt := range d.entries {
		marker := "  "
		if i == d.cursor {
			marker = "▸ "
		}
		branch := wt.Branch
		if branch == "" {
			if wt.Detached {
				branch = "(detached)"
			} else {
				branch = "(no branch)"
			}
		}
		flags := ""
		if wt.Locked {
			flags += " 🔒"
		}
		if wt.Bare {
			flags += " (bare)"
		}
		rendered[i] = fmt.Sprintf("%s%s   %s   %s%s",
			marker,
			lipgloss.NewStyle().Foreground(t.Text()).Render(wt.Path),
			muted.Render(short(wt.HEAD, 8)),
			lipgloss.NewStyle().Foreground(t.Accent()).Render(branch),
			muted.Render(flags),
		)
	}
	visible, before, after := Window(rendered, d.cursor, WindowSize(h))
	var b strings.Builder
	if before > 0 {
		b.WriteString(muted.Render(fmt.Sprintf("  ↑ %d more", before)))
		b.WriteString("\n")
	}
	for _, line := range visible {
		b.WriteString(line)
		b.WriteString("\n")
	}
	if after > 0 {
		b.WriteString(muted.Render(fmt.Sprintf("  ↓ %d more", after)))
		b.WriteString("\n")
	}
	if d.hint != "" {
		b.WriteString("\n")
		b.WriteString(lipgloss.NewStyle().Foreground(t.TextMuted()).Render(d.hint))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(lipgloss.NewStyle().Foreground(t.TextMuted()).Render("[a] add   [j/k] move   [esc] close"))
	return d.BaseView(b.String(), w, h)
}

func short(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

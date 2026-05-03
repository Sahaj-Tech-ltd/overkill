// Package dialog — workspace switcher overlay.
package dialog

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Sahaj-Tech-ltd/ethos/internal/workspace"
	"github.com/Sahaj-Tech-ltd/ethos/pkg/tui/theme"
)

// WorkspaceSwitchMsg fires when the user picks a workspace.
type WorkspaceSwitchMsg struct{ ID string }

// WorkspaceAddMsg fires when the user presses `n` to add the current cwd.
type WorkspaceAddMsg struct{}

// CloseWorkspaceDialogMsg fires on Esc.
type CloseWorkspaceDialogMsg struct{}

// WorkspaceDialog lists known workspaces.
type WorkspaceDialog struct {
	Dialog
	items  []workspace.Workspace
	cursor int
}

func NewWorkspaceDialog() WorkspaceDialog {
	return WorkspaceDialog{Dialog: Dialog{Title: "workspaces"}}
}

// SetWorkspaces installs the data shown.
func (d *WorkspaceDialog) SetWorkspaces(ws []workspace.Workspace) {
	d.items = append([]workspace.Workspace(nil), ws...)
	d.cursor = 0
}

func (d WorkspaceDialog) Update(msg tea.Msg) (WorkspaceDialog, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return d, nil
	}
	switch k.String() {
	case "up", "k":
		if d.cursor > 0 {
			d.cursor--
		}
	case "down", "j":
		if d.cursor < len(d.items)-1 {
			d.cursor++
		}
	case "enter":
		if len(d.items) == 0 {
			return d, nil
		}
		ws := d.items[d.cursor]
		d.Show = false
		return d, func() tea.Msg { return WorkspaceSwitchMsg{ID: ws.ID} }
	case "n":
		return d, func() tea.Msg { return WorkspaceAddMsg{} }
	case "esc":
		d.Show = false
		return d, func() tea.Msg { return CloseWorkspaceDialogMsg{} }
	}
	return d, nil
}

func (d WorkspaceDialog) View(w, h int) string {
	if !d.Show {
		return ""
	}
	t := theme.CurrentTheme()
	hi := lipgloss.NewStyle().Foreground(t.Background()).Background(t.Accent()).Bold(true)
	row := lipgloss.NewStyle().Foreground(t.Text())
	muted := lipgloss.NewStyle().Foreground(t.TextMuted())
	if len(d.items) == 0 {
		return d.BaseView("(no workspaces yet)\n\npress n to add the current directory.\nesc to cancel.", w, h)
	}
	rendered := make([]string, len(d.items))
	for i, ws := range d.items {
		path := ws.Path
		if len(path) > 40 {
			path = "..." + path[len(path)-37:]
		}
		when := humanizeAge(ws.LastUsed)
		line := fmt.Sprintf("%-18s  %s  %s", ws.Name, path, muted.Render(when))
		if i == d.cursor {
			rendered[i] = hi.Render("> " + line)
		} else {
			rendered[i] = row.Render("  " + line)
		}
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
	b.WriteString(muted.Render("\nenter: switch · n: add cwd · esc: close"))
	return d.BaseView(strings.TrimRight(b.String(), "\n"), w, h)
}

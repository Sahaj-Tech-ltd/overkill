package dialog

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Sahaj-Tech-ltd/overkill/deprecated/bubbletea-tui/theme"
	"github.com/Sahaj-Tech-ltd/overkill/internal/mcp"
)

// MCPDialog shows the configured MCP servers, their connection status,
// and the tools each one exposes.
type MCPDialog struct {
	Dialog
	statuses []mcp.ServerStatus
	tools    []mcp.ToolWithServer
}

type CloseMCPDialogMsg struct{}

// MCPRescanMsg fires when the user presses `r` in the dialog. The host
// re-walks every connected server's tool list and registers anything new.
type MCPRescanMsg struct{}

func NewMCPDialog() MCPDialog {
	return MCPDialog{Dialog: Dialog{Title: "MCP Servers"}}
}

func (d *MCPDialog) SetData(statuses []mcp.ServerStatus, tools []mcp.ToolWithServer) {
	d.statuses = statuses
	d.tools = tools
}

func (d MCPDialog) Update(msg tea.Msg) (MCPDialog, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "esc", "q":
			d.Show = false
			return d, func() tea.Msg { return CloseMCPDialogMsg{} }
		case "r":
			return d, func() tea.Msg { return MCPRescanMsg{} }
		}
	}
	return d, nil
}

func (d MCPDialog) View(w, h int) string {
	if !d.Show {
		return ""
	}
	t := theme.CurrentTheme()
	if len(d.statuses) == 0 {
		return d.BaseView("No MCP servers configured.\n\nAdd entries under [mcp] in your overkill config.\n\n[esc] close", w, h)
	}

	muted := lipgloss.NewStyle().Foreground(t.TextMuted())
	rows := make([]string, 0, len(d.statuses)*4)
	for _, s := range d.statuses {
		dot := lipgloss.NewStyle().Foreground(t.Success()).Render("●")
		state := "connected"
		if !s.Connected {
			dot = lipgloss.NewStyle().Foreground(t.Error()).Render("●")
			state = "disconnected"
			if s.LastError != "" {
				state = "error: " + truncate(s.LastError, 60)
			}
		}
		nameStyle := lipgloss.NewStyle().Foreground(t.Text()).Bold(true)
		rows = append(rows, fmt.Sprintf("%s %s   %s   %d tools",
			dot, nameStyle.Render(s.Name),
			muted.Render(state),
			s.ToolsCount,
		))
		for _, tw := range d.tools {
			if tw.Server != s.Name {
				continue
			}
			line := fmt.Sprintf("    • %s", tw.Tool.Name)
			if tw.Tool.Description != "" {
				line += "  " + truncate(tw.Tool.Description, 60)
			}
			rows = append(rows, muted.Render(line))
		}
		rows = append(rows, "")
	}
	visible, before, after := Window(rows, 0, WindowSize(h))
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
	b.WriteString(muted.Render("[r] rescan tools  [esc] close"))
	return d.BaseView(b.String(), w, h)
}

func truncate(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}

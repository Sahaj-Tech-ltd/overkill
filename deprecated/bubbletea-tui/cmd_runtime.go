// Package tui — runtime-toggle slash commands.
//
// /usage, /conceal, /mode, /rollback, /redteam read or flip runtime
// state without scanning the codebase. Each is a single-toast surface
// over a more capable subsystem (cost tracker, render flag, privilege
// gate, checkpoint manager, skill registry).
package tui

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Sahaj-Tech-ltd/overkill/deprecated/bubbletea-tui/styles"
	tuitypes "github.com/Sahaj-Tech-ltd/overkill/deprecated/bubbletea-tui/types"
	"github.com/Sahaj-Tech-ltd/overkill/internal/security"
)

// runUsage prints today's session cost as a toast. The full breakdown
// lives behind `overkill usage` so we don't crowd the chat with a long
// table.
func (m *appModel) runUsage() tea.Cmd {
	if m.app == nil || m.app.Costs == nil {
		return m.toastCmd("usage: cost tracker not configured", "warning")
	}
	if m.app.Agent == nil {
		return m.toastCmd("usage: no active agent", "warning")
	}
	sid := m.app.Agent.SessionID()
	if sid == "" {
		return m.toastCmd("usage: no active session", "info")
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		s, err := m.app.Costs.SessionCost(ctx, sid)
		if err != nil {
			return tuitypes.ToastMsg{Text: "usage: " + err.Error(), Kind: "error"}
		}
		return tuitypes.ToastMsg{
			Text: fmt.Sprintf("usage: $%.4f · in=%d out=%d · %d call(s)",
				s.TotalUSD, s.InputTokens, s.OutputTokens, s.RequestCount),
			Kind: "info",
		}
	}
}

// runConceal toggles raw markdown rendering. Useful when the user wants
// to copy a block out of the chat without ANSI codes.
func (m *appModel) runConceal() tea.Cmd {
	now := !styles.IsConcealMarkdown()
	styles.SetConcealMarkdown(now)
	if now {
		return m.toastCmd("conceal: ON — markdown rendered as raw text", "info")
	}
	return m.toastCmd("conceal: OFF — styled markdown restored", "info")
}

// runMode toggles the agent's privilege mode (reader ↔ writer). Master
// plan §4.3: reader mode blocks every write-like tool call so the
// planning phase can run without surprise mutations.
func (m *appModel) runMode() tea.Cmd {
	if m.app == nil || m.app.Agent == nil {
		return m.toastCmd("mode: agent not running", "warning")
	}
	cur := m.app.Agent.PrivilegeMode()
	switch cur {
	case security.ModeReader:
		m.app.Agent.SetPrivilegeMode(security.ModeWriter)
		return m.toastCmd("mode: writer (writes allowed)", "success")
	case security.ModeWriter:
		m.app.Agent.SetPrivilegeMode(security.ModeReader)
		return m.toastCmd("mode: reader (writes BLOCKED)", "warning")
	default:
		return m.toastCmd("mode: privilege gate not configured", "warning")
	}
}

// runRollback lists filesystem checkpoints for the current session.
// Without arguments it shows the most recent checkpoint IDs as a toast;
// the agent performs the actual restore via the checkpoint_restore
// tool.
func (m *appModel) runRollback() tea.Cmd {
	if m.app == nil || m.app.Checkpoints == nil {
		return m.toastCmd("rollback: checkpoint manager not configured", "warning")
	}
	sid := ""
	if m.app.Agent != nil {
		sid = m.app.Agent.SessionID()
	}
	list, err := m.app.Checkpoints.List(sid)
	if err != nil {
		return m.toastCmd("rollback: "+err.Error(), "error")
	}
	if len(list) == 0 {
		return m.toastCmd("rollback: no checkpoints in this session", "info")
	}
	latest := list[0]
	return m.toastCmd(fmt.Sprintf("rollback: %d checkpoints — latest %s (%s)", len(list), latest.ID, latest.Reason), "info")
}

// runRedteam invokes the bundled red-team skill against the current
// session. Today this surfaces the skill name; full execution requires
// the skill engine to expose a programmatic Run.
func (m *appModel) runRedteam() tea.Cmd {
	if m.app == nil {
		return m.toastCmd("redteam: app not initialised", "warning")
	}
	for _, sk := range m.app.Skills {
		if sk.Name == "red-team" || sk.Name == "redteam" {
			if sk.Enabled {
				return m.toastCmd("redteam: skill loaded — invoke via 'use the red-team skill on this code'", "info")
			}
			return m.toastCmd("redteam: skill present but disabled (toggle in /skills)", "warning")
		}
	}
	return m.toastCmd("redteam: red-team skill not installed", "info")
}

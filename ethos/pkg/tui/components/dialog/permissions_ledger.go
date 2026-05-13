// Package dialog — permissions ledger overlay.
package dialog

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Sahaj-Tech-ltd/overkill/internal/security"
	"github.com/Sahaj-Tech-ltd/overkill/pkg/tui/theme"
)

// LedgerFilter narrows which entries the dialog shows.
type LedgerFilter int

const (
	LedgerFilterAll LedgerFilter = iota
	LedgerFilterAllow
	LedgerFilterDeny
	LedgerFilterSession
)

func (f LedgerFilter) Label() string {
	switch f {
	case LedgerFilterAll:
		return "all"
	case LedgerFilterAllow:
		return "allow"
	case LedgerFilterDeny:
		return "deny"
	case LedgerFilterSession:
		return "session"
	}
	return "?"
}

// ClosePermissionsLedgerMsg fires when the user dismisses the dialog.
type ClosePermissionsLedgerMsg struct{}

// PermissionsLedgerDialog renders the per-session permission decision history.
type PermissionsLedgerDialog struct {
	Dialog
	entries []security.LedgerEntry
	filter  LedgerFilter
	cursor  int
}

func NewPermissionsLedgerDialog() PermissionsLedgerDialog {
	return PermissionsLedgerDialog{Dialog: Dialog{Title: "permissions ledger"}}
}

// SetEntries replaces the entries (keeps current filter).
func (d *PermissionsLedgerDialog) SetEntries(es []security.LedgerEntry) {
	d.entries = append([]security.LedgerEntry(nil), es...)
	d.cursor = 0
}

// Filter returns the active filter.
func (d PermissionsLedgerDialog) Filter() LedgerFilter { return d.filter }

func (d PermissionsLedgerDialog) filtered() []security.LedgerEntry {
	if d.filter == LedgerFilterAll {
		return d.entries
	}
	out := make([]security.LedgerEntry, 0, len(d.entries))
	for _, e := range d.entries {
		switch d.filter {
		case LedgerFilterAllow:
			if strings.HasPrefix(e.Decision, "allow") {
				out = append(out, e)
			}
		case LedgerFilterDeny:
			if e.Decision == "deny" {
				out = append(out, e)
			}
		case LedgerFilterSession:
			if e.Decision == "allow_session" {
				out = append(out, e)
			}
		}
	}
	return out
}

func (d PermissionsLedgerDialog) Update(msg tea.Msg) (PermissionsLedgerDialog, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return d, nil
	}
	rows := d.filtered()
	switch k.String() {
	case "up", "k":
		if d.cursor > 0 {
			d.cursor--
		}
	case "down", "j":
		if d.cursor < len(rows)-1 {
			d.cursor++
		}
	case "tab":
		d.filter = (d.filter + 1) % 4
		d.cursor = 0
	case "esc", "q":
		d.Show = false
		return d, func() tea.Msg { return ClosePermissionsLedgerMsg{} }
	}
	return d, nil
}

func (d PermissionsLedgerDialog) View(w, h int) string {
	if !d.Show {
		return ""
	}
	t := theme.CurrentTheme()
	hi := lipgloss.NewStyle().Foreground(t.Background()).Background(t.Accent()).Bold(true)
	row := lipgloss.NewStyle().Foreground(t.Text())
	muted := lipgloss.NewStyle().Foreground(t.TextMuted())
	allow := lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	deny := lipgloss.NewStyle().Foreground(lipgloss.Color("9"))

	rows := d.filtered()
	header := fmt.Sprintf("filter: [%s]   tab: cycle   esc: close\n\n", d.filter.Label())
	if len(rows) == 0 {
		return d.BaseView(header+"(no entries)", w, h)
	}
	var b strings.Builder
	b.WriteString(header)
	rendered := make([]string, len(rows))
	for i, e := range rows {
		ts := e.Time.Local().Format("15:04:05")
		args := strings.ReplaceAll(e.Args, "\n", " ")
		if len(args) > 40 {
			args = args[:37] + "..."
		}
		decisionStyle := allow
		if e.Decision == "deny" {
			decisionStyle = deny
		}
		decision := decisionStyle.Render(e.Decision)
		line := fmt.Sprintf("%s  %-12s  %s  %s", muted.Render(ts), e.Tool, decision, args)
		if i == d.cursor {
			rendered[i] = hi.Render("> " + line)
		} else {
			rendered[i] = row.Render("  " + line)
		}
	}
	visible, before, after := Window(rendered, d.cursor, WindowSize(h))
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
	b.WriteString(muted.Render(fmt.Sprintf("\n%d entries · last update %s",
		len(d.entries), humanizeAgeShort(time.Now()))))
	return d.BaseView(strings.TrimRight(b.String(), "\n"), w, h)
}

func humanizeAgeShort(t time.Time) string {
	return t.Format("15:04:05")
}

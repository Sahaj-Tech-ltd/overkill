package dialog

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// StatusInfo carries the static (per-snapshot) data the dialog needs to render.
// The TUI populates this immediately before showing the dialog.
type StatusInfo struct {
	ProviderName    string
	ProviderBaseURL string
	ProviderOK      bool
	ModelID         string
	ModelName       string
	MaxTokens       int
	SessionID       string
	SessionTitle    string
	SessionStarted  string
	MessageCount    int
	TotalTokens     int64
	TotalCost       float64
	Tools           []string
	BudgetDailyUsed float64
	BudgetDailyMax  float64
	HookCount       int
}

// CloseStatusDialogMsg dismisses the dialog.
type CloseStatusDialogMsg struct{}

// StatusDialog displays a summary of the currently running overkill instance.
type StatusDialog struct {
	Dialog
	Info StatusInfo
}

// NewStatusDialog returns a fresh dialog (hidden by default).
func NewStatusDialog() StatusDialog {
	return StatusDialog{Dialog: Dialog{Title: "status"}}
}

// SetInfo updates the data shown by the dialog. Called right before Show.
func (s *StatusDialog) SetInfo(info StatusInfo) { s.Info = info }

// Update closes the dialog on esc and consumes other keys silently.
func (s StatusDialog) Update(msg tea.Msg) (StatusDialog, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		if k.String() == "esc" || k.String() == "enter" {
			s.Show = false
			return s, func() tea.Msg { return CloseStatusDialogMsg{} }
		}
	}
	return s, nil
}

// View renders the body. Returns empty when hidden.
func (s StatusDialog) View(totalWidth, totalHeight int) string {
	if !s.Show {
		return ""
	}
	var b strings.Builder

	section := func(label string) {
		fmt.Fprintf(&b, "\n%s\n", label)
	}
	row := func(k, v string) {
		fmt.Fprintf(&b, "  %-10s %s\n", k, v)
	}

	section("provider")
	row("name", fallback(s.Info.ProviderName, "(none)"))
	if s.Info.ProviderBaseURL != "" {
		row("base url", s.Info.ProviderBaseURL)
	}
	state := "down"
	if s.Info.ProviderOK {
		state = "ok"
	}
	row("status", state)

	section("model")
	row("id", fallback(s.Info.ModelID, "(none)"))
	if s.Info.ModelName != "" {
		row("name", s.Info.ModelName)
	}
	if s.Info.MaxTokens > 0 {
		row("max tokens", fmt.Sprintf("%d", s.Info.MaxTokens))
	}

	section("session")
	row("id", fallback(s.Info.SessionID, "(none)"))
	if s.Info.SessionTitle != "" {
		row("title", s.Info.SessionTitle)
	}
	if s.Info.SessionStarted != "" {
		row("started", s.Info.SessionStarted)
	}
	row("messages", fmt.Sprintf("%d", s.Info.MessageCount))
	row("tokens", fmt.Sprintf("%d", s.Info.TotalTokens))
	row("cost", fmt.Sprintf("$%.4f", s.Info.TotalCost))

	section("tools")
	if len(s.Info.Tools) == 0 {
		row("registered", "(none)")
	} else {
		row("registered", strings.Join(s.Info.Tools, ", "))
	}

	section("budget")
	if s.Info.BudgetDailyMax > 0 {
		row("daily", fmt.Sprintf("$%.2f / $%.2f", s.Info.BudgetDailyUsed, s.Info.BudgetDailyMax))
	} else {
		row("daily", "(no limit)")
	}

	section("hooks")
	row("registered", fmt.Sprintf("%d", s.Info.HookCount))

	b.WriteString("\npress esc to close")
	return s.BaseView(strings.TrimSpace(b.String()), totalWidth, totalHeight)
}

func fallback(s, alt string) string {
	if s == "" {
		return alt
	}
	return s
}

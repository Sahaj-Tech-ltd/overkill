package dialog

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Sahaj-Tech-ltd/overkill/internal/browser"
	"github.com/Sahaj-Tech-ltd/overkill/pkg/tui/theme"
)

// BrowserDialog renders the live state of the agentic browser.
type BrowserDialog struct {
	Dialog
	status browser.Status
}

// CloseBrowserDialogMsg is emitted when the user dismisses the dialog.
type CloseBrowserDialogMsg struct{}

// BrowserCloseMsg requests teardown of the headless Chrome process.
type BrowserCloseMsg struct{}

// BrowserRefreshMsg reloads the current page.
type BrowserRefreshMsg struct{}

// BrowserScreenshotMsg requests a screenshot of the current page.
type BrowserScreenshotMsg struct{}

func NewBrowserDialog() BrowserDialog {
	return BrowserDialog{Dialog: Dialog{Title: "Browser"}}
}

func (d *BrowserDialog) SetStatus(s browser.Status) { d.status = s }

func (d BrowserDialog) Update(msg tea.Msg) (BrowserDialog, tea.Cmd) {
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "esc", "q":
			d.Show = false
			return d, func() tea.Msg { return CloseBrowserDialogMsg{} }
		case "c":
			return d, func() tea.Msg { return BrowserCloseMsg{} }
		case "r":
			return d, func() tea.Msg { return BrowserRefreshMsg{} }
		case "s":
			return d, func() tea.Msg { return BrowserScreenshotMsg{} }
		}
	}
	return d, nil
}

func (d BrowserDialog) View(w, h int) string {
	if !d.Show {
		return ""
	}
	t := theme.CurrentTheme()
	muted := lipgloss.NewStyle().Foreground(t.TextMuted())
	bold := lipgloss.NewStyle().Foreground(t.Text()).Bold(true)

	var b strings.Builder
	if !d.status.Running {
		b.WriteString(muted.Render("Browser is not currently running."))
		b.WriteString("\n\n")
		b.WriteString("It will spawn lazily on the first browser_* tool call.\n\n")
		b.WriteString(muted.Render("[esc] close"))
		return d.BaseView(b.String(), w, h)
	}

	dot := lipgloss.NewStyle().Foreground(t.Success()).Render("●")
	b.WriteString(fmt.Sprintf("%s %s   pid=%d   mem=%s\n",
		dot, bold.Render("running"), d.status.PID, formatKB(d.status.MemoryKB)))
	if d.status.Title != "" {
		b.WriteString(bold.Render("title: "))
		b.WriteString(d.status.Title + "\n")
	}
	if d.status.URL != "" {
		b.WriteString(bold.Render("url:   "))
		b.WriteString(d.status.URL + "\n")
	}
	if d.status.UserAgent != "" {
		b.WriteString(muted.Render("ua:    " + d.status.UserAgent + "\n"))
	}
	b.WriteString("\n")
	b.WriteString(muted.Render("[c] close browser  [r] reload  [s] screenshot  [esc] dismiss"))
	return d.BaseView(b.String(), w, h)
}

func formatKB(kb int64) string {
	if kb <= 0 {
		return "?"
	}
	if kb < 1024 {
		return fmt.Sprintf("%dKB", kb)
	}
	return fmt.Sprintf("%.1fMB", float64(kb)/1024.0)
}

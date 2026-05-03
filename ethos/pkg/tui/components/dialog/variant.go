// Package dialog — A/B variant comparison overlay.
package dialog

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Sahaj-Tech-ltd/ethos/internal/agent"
	"github.com/Sahaj-Tech-ltd/ethos/pkg/tui/theme"
)

// VariantPickedMsg fires when the user injects one variant as the assistant
// reply. The other results are discarded.
type VariantPickedMsg struct {
	Index    int
	Response string
	Model    string
}

// CloseVariantDialogMsg fires on Esc.
type CloseVariantDialogMsg struct{}

// VariantDialog shows side-by-side responses for several models.
type VariantDialog struct {
	Dialog
	results []agent.VariantResult
	cursor  int
}

func NewVariantDialog() VariantDialog {
	return VariantDialog{Dialog: Dialog{Title: "variant comparison"}}
}

// SetResults installs the data shown.
func (d *VariantDialog) SetResults(rs []agent.VariantResult) {
	d.results = append([]agent.VariantResult(nil), rs...)
	d.cursor = 0
}

// Results returns a snapshot.
func (d VariantDialog) Results() []agent.VariantResult { return d.results }

func (d VariantDialog) Update(msg tea.Msg) (VariantDialog, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return d, nil
	}
	switch k.String() {
	case "left", "h":
		if d.cursor > 0 {
			d.cursor--
		}
	case "right", "l":
		if d.cursor < len(d.results)-1 {
			d.cursor++
		}
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		idx := int(k.String()[0] - '1')
		if idx >= 0 && idx < len(d.results) {
			r := d.results[idx]
			d.Show = false
			return d, func() tea.Msg {
				return VariantPickedMsg{Index: idx, Response: r.Response, Model: r.Model}
			}
		}
	case "enter":
		if len(d.results) == 0 {
			return d, nil
		}
		r := d.results[d.cursor]
		d.Show = false
		return d, func() tea.Msg {
			return VariantPickedMsg{Index: d.cursor, Response: r.Response, Model: r.Model}
		}
	case "esc":
		d.Show = false
		return d, func() tea.Msg { return CloseVariantDialogMsg{} }
	}
	return d, nil
}

func (d VariantDialog) View(w, h int) string {
	if !d.Show {
		return ""
	}
	t := theme.CurrentTheme()
	muted := lipgloss.NewStyle().Foreground(t.TextMuted())
	hi := lipgloss.NewStyle().Foreground(t.Accent()).Bold(true)
	if len(d.results) == 0 {
		return d.BaseView("(no variants run)", w, h)
	}
	// Stack vertically — much easier to read in terminals than side-by-side.
	var b strings.Builder
	for i, r := range d.results {
		costPart := fmt.Sprintf("$%.4f", r.CostUSD)
		if r.Note != "" {
			costPart = r.Note
		}
		header := fmt.Sprintf("[%d] %s · %dtok · %.1fs · %s", i+1, r.Model, r.Tokens, r.DurationS, costPart)
		if i == d.cursor {
			b.WriteString(hi.Render("▸ " + header))
		} else {
			b.WriteString(muted.Render("  " + header))
		}
		b.WriteString("\n")
		body := r.Response
		if r.Err != "" {
			body = "error: " + r.Err
		}
		body = strings.TrimSpace(body)
		// Keep each variant body short to fit overlay.
		if len(body) > 240 {
			body = body[:237] + "..."
		}
		b.WriteString(indent(body, "    "))
		b.WriteString("\n\n")
	}
	b.WriteString(muted.Render("press 1-9 to pick · enter: pick highlighted · esc: discard"))
	return d.BaseView(strings.TrimRight(b.String(), "\n"), w, h)
}

func indent(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}

// Package sidebar — TodoPanel renders the agent's active plan in
// the right pane. The agent emits the plan via plan_set; this
// panel polls a provider for the current state and renders
// checkable items in their order of declaration.
//
// Pure read-only: the user can't tick items from the TUI here
// (that's the agent's job per the master plan §4.11 flow). A
// future iteration can add user toggling for collaborative
// planning.
package sidebar

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/Sahaj-Tech-ltd/overkill/internal/plan"
)

// PlanProvider supplies the panel with the live plan snapshot.
// The TUI wires this to *plan.Store; tests pass a stub.
type PlanProvider interface {
	Current() *plan.Plan
	Remaining() int
}

// TodoPanel renders the active plan. Nil provider → "No active
// plan" empty-state.
type TodoPanel struct {
	provider PlanProvider
	width    int
	height   int
}

// NewTodoPanel creates a panel bound to the given provider. Pass
// nil to render a perpetual empty state.
func NewTodoPanel(p PlanProvider) TodoPanel {
	return TodoPanel{provider: p}
}

// Name is the tab label shown in the sidebar tab bar.
func (t *TodoPanel) Name() string { return "Plan" }

// View renders the plan inside the given width/height bounds.
func (t *TodoPanel) View(width, height int) string {
	t.width = width
	t.height = height

	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("#6c7086"))
	if t.provider == nil {
		return dim.Render("No active plan")
	}
	p := t.provider.Current()
	if p == nil || len(p.Items) == 0 {
		return dim.Render("No active plan")
	}

	var b strings.Builder

	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#cdd6f4"))
	if p.Title != "" {
		b.WriteString(titleStyle.Render(truncateTodo(p.Title, width)))
		b.WriteByte('\n')
	}

	done := 0
	for _, it := range p.Items {
		if it.Done {
			done++
		}
	}
	headerStyle := dim
	header := fmt.Sprintf("%d/%d done", done, len(p.Items))
	b.WriteString(headerStyle.Render(header))
	b.WriteByte('\n')
	b.WriteByte('\n')

	doneStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#a6e3a1")).Strikethrough(true)
	pendStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#cdd6f4"))
	noteStyle := dim.Italic(true)
	for _, it := range p.Items {
		box := "[ ]"
		style := pendStyle
		if it.Done {
			box = "[x]"
			style = doneStyle
		}
		line := fmt.Sprintf("%s %s", box, it.Text)
		b.WriteString(style.Render(truncateTodo(line, width)))
		b.WriteByte('\n')
		if it.Note != "" {
			noteLine := "    ↳ " + it.Note
			b.WriteString(noteStyle.Render(truncateTodo(noteLine, width)))
			b.WriteByte('\n')
		}
	}

	if t.provider.Remaining() == 0 && len(p.Items) > 0 {
		b.WriteByte('\n')
		b.WriteString(dim.Render("All ticked — record a learning?"))
		b.WriteByte('\n')
	}

	// Tiny footer with last-updated relative timestamp so the user
	// can see the plan is live, not stale.
	if !p.Updated.IsZero() {
		b.WriteByte('\n')
		b.WriteString(dim.Render("updated " + relativeTime(p.Updated)))
	}

	return b.String()
}

// truncateTodo is the panel-local truncate (avoiding collision with
// the package-level truncate in cost.go which uses different
// ellipsis-fallback semantics).
func truncateTodo(s string, width int) string {
	if width <= 0 || len(s) <= width {
		return s
	}
	if width <= 1 {
		return "…"
	}
	return s[:width-1] + "…"
}

func relativeTime(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < 5*time.Second:
		return "just now"
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return t.Format("Jan 2")
	}
}

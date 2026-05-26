// Package dialog — tag picker overlay.
package dialog

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Sahaj-Tech-ltd/overkill/internal/tags"
	"github.com/Sahaj-Tech-ltd/overkill/pkg/tui/theme"
)

// TagSelectedMsg fires when the user picks a tag — the receiver expands it
// into @path mentions for every file with that tag.
type TagSelectedMsg struct {
	Tag   string
	Paths []string
}

// CloseTagDialogMsg fires on Esc.
type CloseTagDialogMsg struct{}

// TagDialog lists tags grouped by name.
type TagDialog struct {
	Dialog
	groups []tagGroup
	cursor int
}

type tagGroup struct {
	Tag   string
	Paths []string
}

func NewTagDialog() TagDialog {
	return TagDialog{Dialog: Dialog{Title: "tags"}}
}

// SetTags installs the data shown in the dialog.
func (d *TagDialog) SetTags(all []tags.Tag) {
	byTag := map[string][]string{}
	order := []string{}
	for _, t := range all {
		if _, ok := byTag[t.Tag]; !ok {
			order = append(order, t.Tag)
		}
		byTag[t.Tag] = append(byTag[t.Tag], t.Path)
	}
	d.groups = d.groups[:0]
	for _, tag := range order {
		d.groups = append(d.groups, tagGroup{Tag: tag, Paths: byTag[tag]})
	}
	d.cursor = 0
}

func (d TagDialog) Update(msg tea.Msg) (TagDialog, tea.Cmd) {
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
		if d.cursor < len(d.groups)-1 {
			d.cursor++
		}
	case "enter":
		if len(d.groups) == 0 {
			return d, nil
		}
		g := d.groups[d.cursor]
		d.Show = false
		return d, func() tea.Msg { return TagSelectedMsg{Tag: g.Tag, Paths: g.Paths} }
	case "esc":
		d.Show = false
		return d, func() tea.Msg { return CloseTagDialogMsg{} }
	}
	return d, nil
}

func (d TagDialog) View(w, h int) string {
	if !d.Show {
		return ""
	}
	t := theme.CurrentTheme()
	hi := lipgloss.NewStyle().Foreground(t.Background()).Background(t.Accent()).Bold(true)
	row := lipgloss.NewStyle().Foreground(t.Text())
	muted := lipgloss.NewStyle().Foreground(t.TextMuted())
	if len(d.groups) == 0 {
		return d.BaseView("(no tags yet)\n\nUse the tag_add tool or /tags to annotate files.", w, h)
	}
	rendered := make([]string, len(d.groups))
	for i, g := range d.groups {
		preview := strings.Join(g.Paths, ", ")
		if len(preview) > 50 {
			preview = preview[:47] + "..."
		}
		line := fmt.Sprintf("@%-12s  %s  %s", g.Tag, muted.Render(fmt.Sprintf("(%d)", len(g.Paths))), preview)
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
	b.WriteString(muted.Render("\nenter: insert @paths · esc: close"))
	return d.BaseView(strings.TrimRight(b.String(), "\n"), w, h)
}

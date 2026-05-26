// Package dialog — installed skill list overlay.
package dialog

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Sahaj-Tech-ltd/overkill/internal/skills"
	"github.com/Sahaj-Tech-ltd/overkill/pkg/tui/theme"
)

// SkillToggleMsg fires when the user toggles a skill enabled/disabled.
type SkillToggleMsg struct {
	Name    string
	Enabled bool
}

// CloseSkillDialogMsg fires on Esc.
type CloseSkillDialogMsg struct{}

// SkillDialog lists installed skills with enable toggles.
type SkillDialog struct {
	Dialog
	skills    []skills.Skill
	cursor    int
	detailFor string
}

func NewSkillDialog() SkillDialog {
	return SkillDialog{Dialog: Dialog{Title: "skills"}}
}

// SetSkills replaces the list.
func (d *SkillDialog) SetSkills(s []skills.Skill) {
	d.skills = append([]skills.Skill(nil), s...)
	d.cursor = 0
	d.detailFor = ""
}

func (d SkillDialog) Update(msg tea.Msg) (SkillDialog, tea.Cmd) {
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
		if d.cursor < len(d.skills)-1 {
			d.cursor++
		}
	case " ":
		if d.cursor >= 0 && d.cursor < len(d.skills) {
			d.skills[d.cursor].Enabled = !d.skills[d.cursor].Enabled
			s := d.skills[d.cursor]
			return d, func() tea.Msg { return SkillToggleMsg{Name: s.Name, Enabled: s.Enabled} }
		}
	case "enter":
		if d.cursor >= 0 && d.cursor < len(d.skills) {
			d.detailFor = d.skills[d.cursor].Name
		}
	case "esc":
		if d.detailFor != "" {
			d.detailFor = ""
			return d, nil
		}
		d.Show = false
		return d, func() tea.Msg { return CloseSkillDialogMsg{} }
	}
	return d, nil
}

func (d SkillDialog) View(w, h int) string {
	if !d.Show {
		return ""
	}
	t := theme.CurrentTheme()
	hi := lipgloss.NewStyle().Foreground(t.Background()).Background(t.Accent()).Bold(true)
	row := lipgloss.NewStyle().Foreground(t.Text())
	muted := lipgloss.NewStyle().Foreground(t.TextMuted())

	if len(d.skills) == 0 {
		return d.BaseView("(no skills installed)", w, h)
	}

	// Detail view
	if d.detailFor != "" {
		for _, s := range d.skills {
			if s.Name == d.detailFor {
				body := s.Instructions
				if len(body) > 600 {
					body = body[:597] + "..."
				}
				out := fmt.Sprintf("%s — %s\nv%s · %s\n\n%s\n\n%s",
					s.Name, s.Description, s.Version, s.Category, body,
					muted.Render("esc: back"))
				return d.BaseView(out, w, h)
			}
		}
	}

	rendered := make([]string, len(d.skills))
	for i, s := range d.skills {
		check := "[ ]"
		if s.Enabled {
			check = "[x]"
		}
		desc := s.Description
		if len(desc) > 36 {
			desc = desc[:33] + "..."
		}
		line := fmt.Sprintf("%s %-18s %s", check, s.Name, desc)
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
	b.WriteString(muted.Render("\nspace: toggle · enter: detail · esc: close"))
	return d.BaseView(strings.TrimRight(b.String(), "\n"), w, h)
}

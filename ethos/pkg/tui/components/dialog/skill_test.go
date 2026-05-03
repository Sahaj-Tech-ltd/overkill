package dialog

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Sahaj-Tech-ltd/ethos/internal/skills"
)

func TestSkillSpaceToggles(t *testing.T) {
	d := NewSkillDialog()
	d.Show = true
	d.SetSkills([]skills.Skill{
		{Name: "alpha", Enabled: false},
	})
	d, cmd := d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" ")})
	if cmd == nil {
		t.Fatal("expected toggle cmd")
	}
	msg := cmd().(SkillToggleMsg)
	if !msg.Enabled || msg.Name != "alpha" {
		t.Errorf("got %+v", msg)
	}
}

func TestSkillEnterShowsDetail(t *testing.T) {
	d := NewSkillDialog()
	d.Show = true
	d.SetSkills([]skills.Skill{{Name: "x", Description: "d", Instructions: "use it"}})
	d, _ = d.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if d.detailFor != "x" {
		t.Errorf("detailFor=%q", d.detailFor)
	}
	v := d.View(80, 24)
	if v == "" {
		t.Errorf("expected detail view")
	}
}

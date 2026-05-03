package dialog

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Sahaj-Tech-ltd/ethos/internal/agent"
)

func TestVariantPickByDigit(t *testing.T) {
	d := NewVariantDialog()
	d.Show = true
	d.SetResults([]agent.VariantResult{
		{Model: "a", Response: "ra"},
		{Model: "b", Response: "rb"},
	})
	d, cmd := d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("2")})
	if cmd == nil {
		t.Fatal("expected cmd")
	}
	msg := cmd().(VariantPickedMsg)
	if msg.Index != 1 || msg.Response != "rb" {
		t.Errorf("got %+v", msg)
	}
}

func TestVariantPickByEnter(t *testing.T) {
	d := NewVariantDialog()
	d.Show = true
	d.SetResults([]agent.VariantResult{{Model: "a", Response: "ra"}})
	d, cmd := d.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected cmd")
	}
	msg := cmd().(VariantPickedMsg)
	if msg.Model != "a" {
		t.Errorf("model=%s", msg.Model)
	}
}

func TestVariantArrowMove(t *testing.T) {
	d := NewVariantDialog()
	d.Show = true
	d.SetResults([]agent.VariantResult{{Model: "a"}, {Model: "b"}})
	d, _ = d.Update(tea.KeyMsg{Type: tea.KeyRight})
	if d.cursor != 1 {
		t.Errorf("cursor=%d", d.cursor)
	}
	d, _ = d.Update(tea.KeyMsg{Type: tea.KeyLeft})
	if d.cursor != 0 {
		t.Errorf("cursor=%d", d.cursor)
	}
}

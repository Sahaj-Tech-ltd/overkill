package dialog

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Sahaj-Tech-ltd/ethos/internal/subagent"
)

func TestSubagentFullCursor(t *testing.T) {
	d := NewSubagentFullDialog()
	d.Show = true
	d.SetChildren([]subagent.ChildRef{
		{ID: "a", Goal: "g1", Status: "running", StartedAt: time.Now()},
		{ID: "b", Goal: "g2", Status: "done", StartedAt: time.Now()},
	})
	d, _ = d.Update(tea.KeyMsg{Type: tea.KeyDown})
	if d.cursor != 1 {
		t.Errorf("cursor=%d", d.cursor)
	}
	d, _ = d.Update(tea.KeyMsg{Type: tea.KeyUp})
	if d.cursor != 0 {
		t.Errorf("cursor=%d", d.cursor)
	}
}

func TestSubagentFullEsc(t *testing.T) {
	d := NewSubagentFullDialog()
	d.Show = true
	_, cmd := d.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("expected close cmd")
	}
}

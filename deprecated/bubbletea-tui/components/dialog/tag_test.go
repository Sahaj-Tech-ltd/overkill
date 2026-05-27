package dialog

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Sahaj-Tech-ltd/overkill/internal/tags"
)

func TestTagDialogGroupsByTagName(t *testing.T) {
	d := NewTagDialog()
	d.Show = true
	d.SetTags([]tags.Tag{
		{Path: "a", Tag: "review"},
		{Path: "b", Tag: "review"},
		{Path: "c", Tag: "todo"},
	})
	if len(d.groups) != 2 {
		t.Fatalf("groups=%d", len(d.groups))
	}
}

func TestTagDialogEnterSelects(t *testing.T) {
	d := NewTagDialog()
	d.Show = true
	d.SetTags([]tags.Tag{{Path: "a", Tag: "x"}, {Path: "b", Tag: "x"}})
	d, cmd := d.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected select cmd")
	}
	msg := cmd()
	sel, ok := msg.(TagSelectedMsg)
	if !ok {
		t.Fatalf("want TagSelectedMsg, got %T", msg)
	}
	if sel.Tag != "x" || len(sel.Paths) != 2 {
		t.Errorf("got %+v", sel)
	}
}

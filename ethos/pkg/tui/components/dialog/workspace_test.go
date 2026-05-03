package dialog

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Sahaj-Tech-ltd/ethos/internal/workspace"
)

func TestWorkspaceEnterSwitches(t *testing.T) {
	d := NewWorkspaceDialog()
	d.Show = true
	d.SetWorkspaces([]workspace.Workspace{
		{ID: "abc", Name: "alpha", Path: "/a", LastUsed: time.Now()},
	})
	d, cmd := d.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected cmd")
	}
	msg := cmd().(WorkspaceSwitchMsg)
	if msg.ID != "abc" {
		t.Errorf("id=%s", msg.ID)
	}
}

func TestWorkspaceNAddsNew(t *testing.T) {
	d := NewWorkspaceDialog()
	d.Show = true
	_, cmd := d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	if cmd == nil {
		t.Fatal("expected add cmd")
	}
	if _, ok := cmd().(WorkspaceAddMsg); !ok {
		t.Errorf("wrong msg type")
	}
}

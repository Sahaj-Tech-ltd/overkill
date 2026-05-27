package dialog

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Sahaj-Tech-ltd/overkill/internal/security"
)

func TestPermissionsLedgerFilterCycle(t *testing.T) {
	d := NewPermissionsLedgerDialog()
	d.Show = true
	d.SetEntries([]security.LedgerEntry{
		{Tool: "shell", Decision: "allow_once", Time: time.Now()},
		{Tool: "fs", Decision: "deny", Time: time.Now()},
		{Tool: "git", Decision: "allow_session", Time: time.Now()},
	})
	if got := len(d.filtered()); got != 3 {
		t.Errorf("all=%d", got)
	}
	d, _ = d.Update(tea.KeyMsg{Type: tea.KeyTab})
	if d.Filter() != LedgerFilterAllow {
		t.Errorf("filter=%v", d.Filter())
	}
	if got := len(d.filtered()); got != 2 {
		t.Errorf("allow=%d", got)
	}
	d, _ = d.Update(tea.KeyMsg{Type: tea.KeyTab})
	if got := len(d.filtered()); got != 1 {
		t.Errorf("deny=%d", got)
	}
	d, _ = d.Update(tea.KeyMsg{Type: tea.KeyTab})
	if got := len(d.filtered()); got != 1 {
		t.Errorf("session=%d", got)
	}
}

func TestPermissionsLedgerEsc(t *testing.T) {
	d := NewPermissionsLedgerDialog()
	d.Show = true
	d, cmd := d.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if d.Show {
		t.Errorf("expected hidden")
	}
	if cmd == nil {
		t.Errorf("expected close cmd")
	}
}

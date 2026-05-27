package dialog

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	tuitypes "github.com/Sahaj-Tech-ltd/overkill/deprecated/bubbletea-tui/types"
)

func TestPermissionDialog_AllowOnce(t *testing.T) {
	reply := make(chan tuitypes.PermissionReply, 1)
	d := NewPermissionDialog()
	d.SetRequest(tuitypes.PermissionRequestMsg{
		ToolName: "shell",
		Args:     "rm -rf /",
		Risk:     "high",
		Reply:    reply,
	})

	d, cmd := d.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected cmd from selection")
	}

	select {
	case ans := <-reply:
		if !ans.Allow || ans.Persist {
			t.Fatalf("expected allow-once, got %+v", ans)
		}
	default:
		t.Fatal("expected reply on channel")
	}
	if d.Show {
		t.Fatal("dialog should hide after selection")
	}
}

func TestPermissionDialog_Deny(t *testing.T) {
	reply := make(chan tuitypes.PermissionReply, 1)
	d := NewPermissionDialog()
	d.SetRequest(tuitypes.PermissionRequestMsg{
		ToolName: "shell",
		Reply:    reply,
	})
	// Move cursor to "deny" (index 2).
	d, _ = d.Update(tea.KeyMsg{Type: tea.KeyDown})
	d, _ = d.Update(tea.KeyMsg{Type: tea.KeyDown})
	d, _ = d.Update(tea.KeyMsg{Type: tea.KeyEnter})

	ans := <-reply
	if ans.Allow {
		t.Fatalf("expected deny, got %+v", ans)
	}
}

func TestPermissionDialog_EscDenies(t *testing.T) {
	reply := make(chan tuitypes.PermissionReply, 1)
	d := NewPermissionDialog()
	d.SetRequest(tuitypes.PermissionRequestMsg{ToolName: "shell", Reply: reply})

	d, _ = d.Update(tea.KeyMsg{Type: tea.KeyEsc})
	ans := <-reply
	if ans.Allow {
		t.Fatal("esc should deny")
	}
	if d.Show {
		t.Fatal("dialog should hide")
	}
}

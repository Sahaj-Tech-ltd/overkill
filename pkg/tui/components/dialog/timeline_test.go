package dialog

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

func TestTimelineDialog_Fork(t *testing.T) {
	d := NewTimelineDialog()
	d.SetMessages([]providers.Message{
		{Role: "user", Content: "first"},
		{Role: "assistant", Content: "ok"},
		{Role: "user", Content: "second"},
	})
	d, _ = d.Update(tea.KeyMsg{Type: tea.KeyDown})
	d, cmd := d.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected fork cmd")
	}
	msg := cmd()
	fork, ok := msg.(TimelineForkMsg)
	if !ok {
		t.Fatalf("expected TimelineForkMsg, got %T", msg)
	}
	if fork.KeepCount != 2 {
		t.Fatalf("expected KeepCount=2, got %d", fork.KeepCount)
	}
	_ = d
}

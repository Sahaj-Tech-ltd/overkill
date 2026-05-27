package dialog

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestStatusDialog_Render(t *testing.T) {
	d := NewStatusDialog()
	d.SetInfo(StatusInfo{
		ProviderName: "openai",
		ModelID:      "gpt-4",
		MessageCount: 3,
	})
	d.Show = true
	out := d.View(80, 24)
	if !strings.Contains(out, "openai") || !strings.Contains(out, "gpt-4") {
		t.Fatalf("expected provider and model in output: %q", out)
	}
}

func TestStatusDialog_EscCloses(t *testing.T) {
	d := NewStatusDialog()
	d.Show = true
	d, cmd := d.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if d.Show {
		t.Fatal("expected hidden after esc")
	}
	if cmd == nil {
		t.Fatal("expected close cmd")
	}
}

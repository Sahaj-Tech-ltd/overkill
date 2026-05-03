package dialog

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Sahaj-Tech-ltd/ethos/internal/plugin"
)

func TestPluginsDialog_RendersStatuses(t *testing.T) {
	d := NewPluginsDialog()
	d.SetData([]plugin.Status{
		{Name: "git-stats", Version: "0.1", Running: true, Tools: 2, Commands: 1},
		{Name: "notes", Version: "0.2", Disabled: true},
	})
	d.Show = true
	out := d.View(80, 24)
	if !strings.Contains(out, "git-stats") || !strings.Contains(out, "notes") {
		t.Fatalf("expected both plugin names in output, got %q", out)
	}
	if !strings.Contains(out, "disabled") {
		t.Fatalf("expected disabled state hint, got %q", out)
	}
}

func TestPluginsDialog_TogglePressEmitsMsg(t *testing.T) {
	d := NewPluginsDialog()
	d.SetData([]plugin.Status{{Name: "git-stats", Running: true}})
	d.Show = true
	_, cmd := d.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}})
	if cmd == nil {
		t.Fatalf("expected toggle to emit a Cmd")
	}
	msg := cmd()
	tog, ok := msg.(PluginToggleMsg)
	if !ok {
		t.Fatalf("expected PluginToggleMsg, got %T", msg)
	}
	if tog.Name != "git-stats" {
		t.Fatalf("expected toggle name git-stats, got %q", tog.Name)
	}
}

func TestPluginsDialog_EscClosesAndEmitsCloseMsg(t *testing.T) {
	d := NewPluginsDialog()
	d.SetData([]plugin.Status{{Name: "git-stats"}})
	d.Show = true
	updated, cmd := d.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if updated.Show {
		t.Fatalf("expected dialog to be hidden after esc")
	}
	if cmd == nil {
		t.Fatalf("expected close cmd")
	}
	if _, ok := cmd().(ClosePluginsDialogMsg); !ok {
		t.Fatalf("expected ClosePluginsDialogMsg")
	}
}

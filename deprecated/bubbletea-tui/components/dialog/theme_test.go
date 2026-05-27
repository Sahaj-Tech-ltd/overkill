package dialog

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Sahaj-Tech-ltd/overkill/deprecated/bubbletea-tui/theme"
)

func TestThemeDialog_Show(t *testing.T) {
	d := NewThemeDialog()
	d, _ = d.Update(ShowThemeDialogMsg{})
	if !d.Show {
		t.Fatal("should be shown")
	}
}

func TestThemeDialog_NavigateAndApply(t *testing.T) {
	original := theme.CurrentTheme()
	defer theme.SetTheme(original)

	d := NewThemeDialog()
	d, _ = d.Update(ShowThemeDialogMsg{})

	// Move cursor down then commit.
	d, _ = d.Update(tea.KeyMsg{Type: tea.KeyDown})
	if d.Cursor != 1 {
		t.Errorf("cursor should advance, got %d", d.Cursor)
	}

	_, cmd := d.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter should emit cmd")
	}
	msg := cmd()
	if _, ok := msg.(ThemeSelectedMsg); !ok {
		t.Fatalf("expected ThemeSelectedMsg, got %T", msg)
	}
}

func TestThemeDialog_EscRevertsTheme(t *testing.T) {
	original := theme.CurrentTheme()
	defer theme.SetTheme(original)

	d := NewThemeDialog()
	d, _ = d.Update(ShowThemeDialogMsg{})
	// Preview second theme, then esc → expect revert to original.
	d, _ = d.Update(tea.KeyMsg{Type: tea.KeyDown})
	d, _ = d.Update(tea.KeyMsg{Type: tea.KeyEsc})

	if theme.CurrentTheme() != original {
		t.Error("esc should revert theme to the one active before opening")
	}
}

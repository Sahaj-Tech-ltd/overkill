package theme

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestTheme_ColorSlots(t *testing.T) {
	th := &Catppuccin{}
	methods := []func() lipgloss.Color{
		th.Background, th.Foreground, th.Text, th.TextMuted, th.TextBold,
		th.Primary, th.Secondary, th.Accent, th.Success, th.Warning, th.Error,
		th.Border, th.BorderFocused, th.BorderUnfocused,
		th.PanelBackground, th.PanelBorder, th.PanelActive, th.PanelInactive,
		th.EditorBackground, th.EditorBorder, th.EditorCursor, th.EditorPlaceholder,
		th.StatusBarBackground, th.StatusBarText, th.StatusBarBorder,
		th.DialogBackground, th.DialogBorder, th.DialogText, th.DialogAccent, th.DialogHighlight,
		th.MessageUserBackground, th.MessageUserText,
		th.MessageAssistantBackground, th.MessageAssistantText,
		th.MessageToolBackground, th.MessageToolText,
		th.MessageErrorText,
		th.SidebarBackground, th.SidebarBorder,
		th.SidebarActiveTab, th.SidebarInactiveTab,
	}
	for i, m := range methods {
		if m() == "" {
			t.Errorf("slot %d returned empty", i)
		}
	}
}

func TestTheme_CurrentTheme(t *testing.T) {
	th := CurrentTheme()
	if th == nil {
		t.Fatal("CurrentTheme returned nil")
	}
}

func TestTheme_CatppuccinDefaults(t *testing.T) {
	c := &Catppuccin{}
	if c.Background() != "#1e1e2e" {
		t.Error("background")
	}
	if c.Text() != "#cdd6f4" {
		t.Error("text")
	}
	if c.Primary() != "#89b4fa" {
		t.Error("primary")
	}
	if c.Error() != "#f38ba8" {
		t.Error("error")
	}
}

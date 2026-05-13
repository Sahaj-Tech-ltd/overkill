package tui

import (
	"testing"
)

func TestResolveEditor_VisualWins(t *testing.T) {
	t.Setenv("VISUAL", "code --wait")
	t.Setenv("EDITOR", "nano")
	if got := resolveEditor(); got != "code --wait" {
		t.Errorf("VISUAL should win: got %q", got)
	}
}

func TestResolveEditor_EditorFallback(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "nano")
	if got := resolveEditor(); got != "nano" {
		t.Errorf("EDITOR fallback: got %q", got)
	}
}

func TestResolveEditor_ViDefault(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "")
	if got := resolveEditor(); got != "vi" {
		t.Errorf("vi default: got %q", got)
	}
}

func TestResolveEditor_WhitespaceTreatedAsUnset(t *testing.T) {
	t.Setenv("VISUAL", "   ")
	t.Setenv("EDITOR", "nano")
	if got := resolveEditor(); got != "nano" {
		t.Errorf("whitespace-only VISUAL should be ignored: got %q", got)
	}
}

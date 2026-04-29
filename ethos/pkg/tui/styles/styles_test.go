package styles

import (
	"strings"
	"testing"
)

func TestStyles_BaseStyle(t *testing.T) {
	s := BaseStyle()
	w := s.GetHorizontalPadding()
	if w != 2 {
		t.Errorf("expected padding 2, got %d", w)
	}
}

func TestStyles_BorderStyle(t *testing.T) {
	s := BorderStyle(true)
	rendered := s.Render("test")
	if !strings.Contains(rendered, "╭") && !strings.Contains(rendered, "─") {
		t.Error("expected rounded border chars")
	}
}

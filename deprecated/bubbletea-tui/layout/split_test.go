package layout

import (
	"strings"
	"testing"
)

func TestSplit_Horizontal(t *testing.T) {
	result := HorizontalSplit("AAA", "BBB", 0.7, 100)
	leftLen := strings.Index(result, "BBB")
	if leftLen < 60 || leftLen > 80 {
		t.Errorf("expected ~70, got %d", leftLen)
	}
}

func TestSplit_Vertical(t *testing.T) {
	result := VerticalSplit("top", "bottom", 2)
	lines := strings.Split(result, "\n")
	if len(lines) < 2 {
		t.Error("expected at least 2 lines")
	}
}

func TestSplit_ZeroWidth(t *testing.T) {
	result := HorizontalSplit("AAA", "BBB", 0.0, 100)
	if strings.Contains(result, "AAA") {
		t.Error("left should be hidden")
	}
}

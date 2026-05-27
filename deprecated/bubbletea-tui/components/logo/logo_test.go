package logo

import (
	"strings"
	"testing"
)

func TestLogo_RenderHasFourRows(t *testing.T) {
	out := Render(nil)
	rows := strings.Split(out, "\n")
	if len(rows) != Height() {
		t.Fatalf("expected %d rows, got %d", Height(), len(rows))
	}
}

func TestLogo_RenderNonEmpty(t *testing.T) {
	if Render(nil) == "" {
		t.Fatal("logo must render non-empty")
	}
}

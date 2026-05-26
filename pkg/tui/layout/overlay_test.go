package layout

import (
	"strings"
	"testing"
)

func TestOverlay_Center(t *testing.T) {
	bg := strings.Repeat("x\n", 10)
	overlay := "hi"
	result := PlaceOverlay(5, 5, overlay, bg, false)
	if !strings.Contains(result, "hi") {
		t.Error("overlay not in result")
	}
}

func TestOverlay_OffScreen(t *testing.T) {
	bg := "x"
	overlay := strings.Repeat("y\n", 100)
	result := PlaceOverlay(0, 0, overlay, bg, false)
	_ = result
}

package logo

import (
	"os"
	"strings"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/pkg/tui/components/animation"
)

func TestShimmer_BrightnessCurveMonotonic(t *testing.T) {
	c := BrightnessCurveCopy()
	if len(c) < 2 {
		t.Fatal("curve too small")
	}
	if c[0] != 1.0 {
		t.Fatalf("curve[0] should be 1.0, got %v", c[0])
	}
	for i := 1; i < len(c); i++ {
		if c[i] > c[i-1] {
			t.Fatalf("brightness curve must be non-increasing: c[%d]=%v > c[%d]=%v",
				i, c[i], i-1, c[i-1])
		}
	}
	last := c[len(c)-1]
	if last != 0.0 {
		t.Fatalf("curve tail should reach 0, got %v", last)
	}
}

func TestShimmer_FrameIndexWraps(t *testing.T) {
	a := RenderShimmer(nil, 0)
	b := RenderShimmer(nil, ShimmerFrames)
	if a != b {
		t.Fatalf("frame %d should equal frame 0", ShimmerFrames)
	}
	c := RenderShimmer(nil, -1)
	d := RenderShimmer(nil, ShimmerFrames-1)
	if c != d {
		t.Fatalf("negative frame should wrap to %d", ShimmerFrames-1)
	}
}

func TestShimmer_AnimationOffEqualsStatic(t *testing.T) {
	os.Setenv("OVERKILL_NO_ANIMATIONS", "1")
	defer os.Unsetenv("OVERKILL_NO_ANIMATIONS")
	animation.SetEnabled(true)

	m := NewLogoModel()
	m.SetWidth(120)
	if got, want := m.View(), Render(nil); got != want {
		t.Fatalf("animation-off view should equal static render\n got: %q\nwant: %q", got, want)
	}
	if cmd := m.Init(); cmd != nil {
		t.Fatal("Init should return nil when animations disabled")
	}
}

func TestShimmer_RenderProducesAllRows(t *testing.T) {
	out := RenderShimmer(nil, 5)
	rows := strings.Split(out, "\n")
	if len(rows) != Height() {
		t.Fatalf("got %d rows, want %d", len(rows), Height())
	}
}

func TestShimmer_HexBlend(t *testing.T) {
	out := blendColors("#000000", "#ffffff", 0.5)
	r, g, b, ok := hexToRGB(string(out))
	if !ok {
		t.Fatalf("output not hex: %q", out)
	}
	// 0x7f or 0x80 acceptable due to rounding.
	if r < 0x7e || r > 0x80 || g < 0x7e || g > 0x80 || b < 0x7e || b > 0x80 {
		t.Fatalf("midpoint blend wrong: %02x%02x%02x", r, g, b)
	}
}

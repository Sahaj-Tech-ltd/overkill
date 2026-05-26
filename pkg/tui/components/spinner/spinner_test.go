package spinner

import (
	"testing"
)

func TestSpinnerStartStop(t *testing.T) {
	s := New()
	if s.IsActive() {
		t.Fatal("new spinner should be inactive")
	}
	cmd := s.Start()
	if cmd == nil {
		t.Fatal("Start should return a tick cmd")
	}
	if !s.IsActive() {
		t.Fatal("spinner should be active after Start")
	}
	s.Stop()
	if s.IsActive() {
		t.Fatal("spinner should be inactive after Stop")
	}
}

func TestSpinnerAdvancesFrame(t *testing.T) {
	s := New()
	s.Start()
	startFrame := s.frame
	s, _ = s.Update(TickMsg{})
	if s.frame == startFrame {
		t.Fatalf("frame should advance from %d", startFrame)
	}
}

func TestSpinnerViewWhenInactive(t *testing.T) {
	s := New()
	if s.View() != "" {
		t.Fatal("inactive spinner should render empty")
	}
}

func TestSpinnerViewWithLabel(t *testing.T) {
	s := New()
	s.Start()
	s.SetLabel("thinking")
	if s.View() == "" {
		t.Fatal("active spinner should render frame and label")
	}
}

func TestSpinnerVariants_FrameCounts(t *testing.T) {
	cases := []struct {
		name string
		v    Variant
		want int
	}{
		{"Braille", Braille, 10},
		{"Dots", Dots, 8},
		{"Line", Line, 4},
		{"Pulse", Pulse, 8},
		{"Bounce", Bounce, 8},
		{"Arrow", Arrow, 8},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := FramesFor(tc.v)
			if len(f) != tc.want {
				t.Fatalf("variant %s: got %d frames, want %d", tc.name, len(f), tc.want)
			}
		})
	}
}

func TestSpinnerVariants_CycleThroughFrames(t *testing.T) {
	for _, v := range []Variant{Braille, Dots, Line, Pulse, Bounce, Arrow} {
		s := New(WithVariant(v))
		s.Start()
		frames := FramesFor(v)
		seen := make(map[int]bool)
		for i := 0; i < len(frames)*2; i++ {
			seen[s.Frame()] = true
			s, _ = s.Update(TickMsg{})
		}
		if len(seen) != len(frames) {
			t.Fatalf("variant %v: expected to see %d distinct frames, saw %d", v, len(frames), len(seen))
		}
	}
}

func TestSpinnerVariant_Default(t *testing.T) {
	s := New()
	if s.Variant() != Braille {
		t.Fatalf("default variant should be Braille, got %v", s.Variant())
	}
}

func TestSpinnerVariant_WithVariant(t *testing.T) {
	s := New(WithVariant(Pulse))
	if s.Variant() != Pulse {
		t.Fatal("WithVariant should set the variant")
	}
}

func TestFramesFor_UnknownFallsBackToBraille(t *testing.T) {
	got := FramesFor(Variant(99))
	if len(got) != len(Frames) {
		t.Fatal("unknown variant should fall back to Braille")
	}
}

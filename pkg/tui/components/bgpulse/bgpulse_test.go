package bgpulse

import (
	"os"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/pkg/tui/components/animation"
)

func TestPulse_StartStop(t *testing.T) {
	animation.SetEnabled(true)
	os.Unsetenv("OVERKILL_NO_ANIMATIONS")

	m := New()
	m.SetWidth(120)
	if cmd := m.Start(); cmd == nil {
		t.Fatal("Start should return tick cmd when enabled")
	}
	if !m.Active() {
		t.Fatal("expected active")
	}
	m.Stop()
	if m.Active() {
		t.Fatal("Stop should clear active")
	}
}

func TestPulse_StartGatedOff(t *testing.T) {
	animation.SetEnabled(false)
	defer animation.SetEnabled(true)
	m := New()
	m.SetWidth(120)
	if cmd := m.Start(); cmd != nil {
		t.Fatal("Start must be no-op when disabled")
	}
	if m.Active() {
		t.Fatal("Active must remain false")
	}
}

func TestPulse_TickAdvances(t *testing.T) {
	animation.SetEnabled(true)
	os.Unsetenv("OVERKILL_NO_ANIMATIONS")
	m := New()
	m.SetWidth(120)
	m.Start()
	prev := m.Frame()
	m, cmd := m.Update(TickMsg{})
	if cmd == nil {
		t.Fatal("active tick should re-arm")
	}
	if m.Frame() == prev {
		t.Fatal("frame should advance")
	}
}

func TestPulse_TickDroppedWhenInactive(t *testing.T) {
	animation.SetEnabled(true)
	m := New()
	m.SetWidth(120)
	m, cmd := m.Update(TickMsg{})
	if cmd != nil {
		t.Fatal("inactive tick should not re-arm")
	}
	if m.Frame() != 0 {
		t.Fatal("inactive frame should not advance")
	}
}

func TestPulse_IntensityRange(t *testing.T) {
	for f := 0; f < PulseFrames; f++ {
		v := pulseIntensity(f)
		if v < 0 || v > 1 {
			t.Fatalf("intensity out of range at frame %d: %v", f, v)
		}
	}
	if pulseIntensity(0) != pulseIntensity(PulseFrames) {
		t.Fatal("intensity should wrap at PulseFrames")
	}
}

func TestPulse_StyleRendersSomething(t *testing.T) {
	animation.SetEnabled(true)
	os.Unsetenv("OVERKILL_NO_ANIMATIONS")
	s := Style(nil, 3).Render("hi")
	if s == "" {
		t.Fatal("style should render content")
	}
}

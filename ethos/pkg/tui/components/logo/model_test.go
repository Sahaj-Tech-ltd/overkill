package logo

import (
	"os"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/pkg/tui/components/animation"
)

func TestLogoModel_StartTicksOnlyWhenEnabled(t *testing.T) {
	os.Unsetenv("ETHOS_NO_ANIMATIONS")
	animation.SetEnabled(true)

	m := NewLogoModel()
	m.SetWidth(120)
	cmd := m.Start()
	if cmd == nil {
		t.Fatal("Start should return tick cmd when enabled")
	}
	if !m.IsActive() {
		t.Fatal("model should be active after Start")
	}
	m.Stop()
	if m.IsActive() {
		t.Fatal("model should be inactive after Stop")
	}
}

func TestLogoModel_StartNoOpWhenNarrow(t *testing.T) {
	animation.SetEnabled(true)
	os.Unsetenv("ETHOS_NO_ANIMATIONS")

	m := NewLogoModel()
	m.SetWidth(20)
	if cmd := m.Start(); cmd != nil {
		t.Fatal("narrow term must not start ticking")
	}
	if m.IsActive() {
		t.Fatal("narrow term must not flip active")
	}
}

func TestLogoModel_TickAdvancesFrame(t *testing.T) {
	animation.SetEnabled(true)
	os.Unsetenv("ETHOS_NO_ANIMATIONS")

	m := NewLogoModel()
	m.SetWidth(120)
	m.Start()
	prev := m.Frame()
	m, _ = m.Update(ShimmerTickMsg{})
	if m.Frame() == prev {
		t.Fatal("frame should advance on tick")
	}
}

func TestLogoModel_TickDroppedWhenInactive(t *testing.T) {
	animation.SetEnabled(true)
	m := NewLogoModel()
	m.SetWidth(120)
	// not started
	m, cmd := m.Update(ShimmerTickMsg{})
	if cmd != nil {
		t.Fatal("inactive model should not re-arm")
	}
	if m.Frame() != 0 {
		t.Fatal("inactive frame should not advance")
	}
}

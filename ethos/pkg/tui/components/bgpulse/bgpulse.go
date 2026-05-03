// Package bgpulse renders a subtle, sine-wave background pulse used while
// the agent is actively generating. Inspired by opencode's bg-pulse but
// adapted to lipgloss (no real alpha — we precompute a dim primary tint
// and blend toward it).
package bgpulse

import (
	"image/color"
	"math"
	"strconv"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Sahaj-Tech-ltd/ethos/pkg/tui/components/animation"
	"github.com/Sahaj-Tech-ltd/ethos/pkg/tui/theme"
)

// PulseFrames is the number of frames in one full sine cycle.
// At 6 FPS this yields ~1.6 seconds, matching opencode's pacing.
const PulseFrames = 10

// pulseInterval gives ~6 FPS. Background pulses are ambient — we keep this
// well under the SSH-conscious budget.
const pulseInterval = 166 * time.Millisecond

// pulseMaxMix is the maximum blend ratio toward the primary tint. Kept
// small so the effect reads as a breath of color, never a flash.
const pulseMaxMix = 0.08

// TickMsg fires once per pulse frame.
type TickMsg struct{}

// Model is a bubble-tea model for the background pulse. It only ticks
// while Active() and only renders a non-default background while the
// animation gate is open.
type Model struct {
	frame  int
	active bool
	width  int
}

// New constructs an inactive pulse.
func New() Model { return Model{} }

// SetWidth lets the model honor the narrow-terminal kill switch.
func (m *Model) SetWidth(w int) { m.width = w }

// Frame returns the current frame index.
func (m Model) Frame() int { return m.frame }

// Active reports whether the pulse is ticking.
func (m Model) Active() bool { return m.active }

// Start activates the pulse and returns the first tick command. Returns
// nil if animations are gated off.
func (m *Model) Start() tea.Cmd {
	if !animation.Enabled(m.width) {
		return nil
	}
	m.active = true
	return tick()
}

// Stop halts ticks. Already-in-flight ticks are dropped on arrival.
func (m *Model) Stop() { m.active = false }

// Update advances the frame on each TickMsg and re-arms while active.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case TickMsg:
		if !m.active {
			return m, nil
		}
		if !animation.Enabled(m.width) {
			m.active = false
			return m, nil
		}
		m.frame = (m.frame + 1) % PulseFrames
		return m, tick()
	case tea.WindowSizeMsg:
		m.width = msg.Width
	}
	return m, nil
}

// Style returns a lipgloss.Style with the pulsed background color for the
// given theme and frame. When animations are off the plain theme background
// is returned, so callers can wire this in unconditionally.
func Style(t theme.Theme, frame int) lipgloss.Style {
	if t == nil {
		t = theme.CurrentTheme()
	}
	base := lipgloss.NewStyle().Background(t.MessageAssistantBackground())
	if !animation.Enabled(0) {
		return base
	}
	intensity := pulseIntensity(frame)
	bg := blend(t.MessageAssistantBackground(), t.Primary(), intensity*pulseMaxMix)
	return lipgloss.NewStyle().Background(bg)
}

// View is a convenience that renders text inside the pulsed style.
func (m Model) View(t theme.Theme, body string) string {
	return Style(t, m.frame).Render(body)
}

// pulseIntensity is a positive sine on [0, 1] mapped from the frame index.
// Exposed for tests.
func pulseIntensity(frame int) float64 {
	if PulseFrames <= 0 {
		return 0
	}
	frame = ((frame % PulseFrames) + PulseFrames) % PulseFrames
	theta := 2 * math.Pi * float64(frame) / float64(PulseFrames)
	// (1 + sin) / 2 keeps us in [0, 1]
	return (1.0 + math.Sin(theta)) / 2.0
}

func tick() tea.Cmd {
	return tea.Tick(pulseInterval, func(time.Time) tea.Msg { return TickMsg{} })
}

func blend(a, b lipgloss.Color, ratio float64) lipgloss.Color {
	if ratio <= 0 {
		return a
	}
	if ratio >= 1 {
		return b
	}
	ar, ag, ab, ok1 := hexToRGB(string(a))
	br, bg, bb, ok2 := hexToRGB(string(b))
	if !ok1 || !ok2 {
		return a
	}
	mix := func(x, y uint8) uint8 {
		return uint8(float64(x)*(1-ratio) + float64(y)*ratio)
	}
	c := color.RGBA{R: mix(ar, br), G: mix(ag, bg), B: mix(ab, bb), A: 0xff}
	return lipgloss.Color(rgbToHex(c))
}

func hexToRGB(s string) (uint8, uint8, uint8, bool) {
	if len(s) == 0 || s[0] != '#' || (len(s) != 7 && len(s) != 4) {
		return 0, 0, 0, false
	}
	if len(s) == 4 {
		s = "#" + string(s[1]) + string(s[1]) + string(s[2]) + string(s[2]) + string(s[3]) + string(s[3])
	}
	parse := func(off int) (uint8, bool) {
		v, err := strconv.ParseUint(s[off:off+2], 16, 8)
		if err != nil {
			return 0, false
		}
		return uint8(v), true
	}
	r, ok1 := parse(1)
	g, ok2 := parse(3)
	b, ok3 := parse(5)
	if !ok1 || !ok2 || !ok3 {
		return 0, 0, 0, false
	}
	return r, g, b, true
}

func rgbToHex(c color.RGBA) string {
	const hex = "0123456789abcdef"
	buf := []byte{'#', 0, 0, 0, 0, 0, 0}
	put := func(off int, v uint8) {
		buf[off] = hex[v>>4]
		buf[off+1] = hex[v&0x0f]
	}
	put(1, c.R)
	put(3, c.G)
	put(5, c.B)
	return string(buf)
}

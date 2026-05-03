package logo

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Sahaj-Tech-ltd/ethos/pkg/tui/components/animation"
	"github.com/Sahaj-Tech-ltd/ethos/pkg/tui/theme"
)

// shimmerInterval gives ~8 FPS — well below the SSH-conscious budget.
const shimmerInterval = 125 * time.Millisecond

// ShimmerTickMsg fires once per shimmer frame.
type ShimmerTickMsg struct{}

// LogoModel is the animated logo. When animations are gated off it
// renders the static logo and emits no tick commands.
type LogoModel struct {
	frame  int
	active bool
	width  int
}

// NewLogoModel builds an inactive logo model. Call Start() to begin the
// shimmer; the parent must keep forwarding ShimmerTickMsg.
func NewLogoModel() LogoModel { return LogoModel{} }

// SetWidth tells the model the current terminal width so it can decide
// whether the animation gate is open.
func (m *LogoModel) SetWidth(w int) { m.width = w }

// Frame returns the current shimmer frame index (0..ShimmerFrames-1).
func (m LogoModel) Frame() int { return m.frame }

// IsActive reports whether the shimmer is currently animating.
func (m LogoModel) IsActive() bool { return m.active }

// Init satisfies tea.Model. It only kicks the timer when animations are
// allowed; otherwise the logo stays static.
func (m LogoModel) Init() tea.Cmd {
	if !animation.Enabled(m.width) {
		return nil
	}
	return shimmerTick()
}

// Start activates the shimmer and returns the first tick command.
// Returns nil (no-op) when animations are gated off.
func (m *LogoModel) Start() tea.Cmd {
	if !animation.Enabled(m.width) {
		return nil
	}
	m.active = true
	return shimmerTick()
}

// Stop halts the shimmer. Pending ticks are dropped on receipt.
func (m *LogoModel) Stop() { m.active = false }

// Update advances the frame on each ShimmerTickMsg and re-arms the timer
// while active. Window-size messages refresh the cached width.
func (m LogoModel) Update(msg tea.Msg) (LogoModel, tea.Cmd) {
	switch msg := msg.(type) {
	case ShimmerTickMsg:
		if !m.active {
			return m, nil
		}
		if !animation.Enabled(m.width) {
			m.active = false
			return m, nil
		}
		m.frame = (m.frame + 1) % ShimmerFrames
		return m, shimmerTick()
	case tea.WindowSizeMsg:
		m.width = msg.Width
	}
	return m, nil
}

// View renders the logo at the current frame, or the static logo when
// animations are disabled / the gate is closed.
func (m LogoModel) View() string {
	t := theme.CurrentTheme()
	if !animation.Enabled(m.width) {
		return Render(t)
	}
	return RenderShimmer(t, m.frame)
}

func shimmerTick() tea.Cmd {
	return tea.Tick(shimmerInterval, func(time.Time) tea.Msg { return ShimmerTickMsg{} })
}

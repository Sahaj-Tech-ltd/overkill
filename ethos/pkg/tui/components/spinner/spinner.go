// Package spinner provides a tiny animated spinner with several visual
// variants reusable across the TUI. Variants share the same model and tick
// cadence so the SSH render budget stays predictable.
package spinner

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Sahaj-Tech-ltd/ethos/pkg/tui/theme"
)

// Variant selects the frame set used by a spinner.
type Variant int

const (
	// Braille is the default opencode-style braille rotation.
	Braille Variant = iota
	// Dots is a denser braille rotation, useful for heavier loading states.
	Dots
	// Line is the classic ASCII line spinner; safe on every terminal.
	Line
	// Pulse expands and contracts symmetrically through block shading.
	Pulse
	// Bounce moves a single dot around the cell perimeter.
	Bounce
	// Arrow rotates through the eight cardinal/intercardinal directions.
	Arrow
)

// Frames is preserved as the historical default braille frame set so
// existing callers (status bar) keep rendering identically.
var Frames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

var variantFrames = map[Variant][]string{
	Braille: Frames,
	Dots:    {"⣾", "⣽", "⣻", "⢿", "⡿", "⣟", "⣯", "⣷"},
	Line:    {"-", "\\", "|", "/"},
	Pulse:   {"█", "▓", "▒", "░", " ", "░", "▒", "▓"},
	Bounce:  {"⠁", "⠂", "⠄", "⡀", "⢀", "⠠", "⠐", "⠈"},
	Arrow:   {"←", "↖", "↑", "↗", "→", "↘", "↓", "↙"},
}

// FramesFor returns the frame slice for the given variant. Falls back to
// Braille for unknown variants so callers can never accidentally render
// nothing.
func FramesFor(v Variant) []string {
	if frames, ok := variantFrames[v]; ok {
		return frames
	}
	return Frames
}

const tickInterval = 80 * time.Millisecond

// TickMsg fires on each spinner advance.
type TickMsg struct{}

// Option configures the spinner at construction time.
type Option func(*Model)

// WithVariant selects a spinner variant.
func WithVariant(v Variant) Option {
	return func(m *Model) { m.variant = v }
}

// Model is a minimal animated spinner. Embed it in a parent model and
// forward TickMsg to its Update.
type Model struct {
	frame   int
	active  bool
	label   string
	variant Variant
}

// New constructs an inactive spinner. Pass options to override the variant.
func New(opts ...Option) Model {
	m := Model{}
	for _, opt := range opts {
		opt(&m)
	}
	return m
}

// Variant returns the currently selected variant.
func (m Model) Variant() Variant { return m.variant }

// Start activates the spinner and returns the first tick command.
func (m *Model) Start() tea.Cmd {
	m.active = true
	return tick()
}

// Stop halts the spinner. The next tick is dropped on receipt.
func (m *Model) Stop() { m.active = false }

// SetLabel sets a short status label rendered next to the spinner.
func (m *Model) SetLabel(s string) { m.label = s }

// IsActive reports whether the spinner is currently animating.
func (m Model) IsActive() bool { return m.active }

// Frame returns the current frame index (0..len(frames)-1).
func (m Model) Frame() int { return m.frame }

// Update advances the frame on each TickMsg and re-arms the timer while
// active.
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	if _, ok := msg.(TickMsg); ok {
		if !m.active {
			return m, nil
		}
		frames := FramesFor(m.variant)
		m.frame = (m.frame + 1) % len(frames)
		return m, tick()
	}
	return m, nil
}

// View renders the current frame and (optionally) label.
func (m Model) View() string {
	if !m.active {
		return ""
	}
	t := theme.CurrentTheme()
	frames := FramesFor(m.variant)
	frame := lipgloss.NewStyle().Foreground(t.Primary()).Render(frames[m.frame])
	if m.label == "" {
		return frame
	}
	label := lipgloss.NewStyle().Foreground(t.Text()).Render(m.label)
	return frame + " " + label
}

func tick() tea.Cmd {
	return tea.Tick(tickInterval, func(time.Time) tea.Msg { return TickMsg{} })
}

package status

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Sahaj-Tech-ltd/overkill/deprecated/bubbletea-tui/components/animation"
)

// SlideFrames is the number of discrete frames in a slide-in or slide-out
// animation. 4 frames over 250ms = ~16 FPS, which is fine because the
// window is short and bounded (no idle ticking).
const SlideFrames = 4

// slideTickInterval gives one frame every ~62ms; total slide ≈ 250ms.
const slideTickInterval = 62 * time.Millisecond

type slideState int

const (
	slideHidden slideState = iota
	slideIn
	slideShown
	slideOut
)

type ToastModel struct {
	message string
	visible bool
	timer   time.Duration
	width   int

	// Animation state
	slideState slideState
	slideStep  int // 0..SlideFrames
}

func NewToastModel() ToastModel {
	return ToastModel{timer: 5 * time.Second}
}

// SetWidth lets the toast honor the narrow-terminal animation gate and
// bound its slide offset.
func (t *ToastModel) SetWidth(w int) { t.width = w }

func ShowToast(msg string) tea.Cmd {
	return func() tea.Msg {
		return toastShowMsg{message: msg}
	}
}

type toastShowMsg struct {
	message string
}

// ShowMsgFromText returns a public toast-show msg builder so external packages
// can drive the toast component without importing internal types.
func ShowMsgFromText(text string) tea.Msg {
	return toastShowMsg{message: text}
}

type toastHideMsg struct{}

// toastSlideTickMsg is the per-frame tick used during the slide windows.
type toastSlideTickMsg struct{}

func (t ToastModel) Init() tea.Cmd { return nil }

// SlidePos returns the current slide position in [0, 1]. 0 = fully off
// the right edge, 1 = fully docked. Exposed for tests.
func (t ToastModel) SlidePos() float64 {
	if t.slideState == slideHidden {
		return 0
	}
	if t.slideState == slideShown {
		return 1
	}
	step := float64(t.slideStep) / float64(SlideFrames)
	if step < 0 {
		step = 0
	}
	if step > 1 {
		step = 1
	}
	if t.slideState == slideOut {
		// When sliding out we reverse the position so callers see 1 → 0.
		return 1 - step
	}
	return step
}

func (t ToastModel) Update(msg tea.Msg) (ToastModel, tea.Cmd) {
	switch m := msg.(type) {
	case toastShowMsg:
		t.message = m.message
		t.visible = true
		// Skip the slide entirely when animations are gated off.
		if !animation.Enabled(t.width) {
			t.slideState = slideShown
			t.slideStep = SlideFrames
			return t, tea.Tick(t.timer, func(time.Time) tea.Msg { return toastHideMsg{} })
		}
		t.slideState = slideIn
		t.slideStep = 0
		return t, slideTick()

	case toastSlideTickMsg:
		switch t.slideState {
		case slideIn:
			t.slideStep++
			if t.slideStep >= SlideFrames {
				t.slideStep = SlideFrames
				t.slideState = slideShown
				return t, tea.Tick(t.timer, func(time.Time) tea.Msg { return toastHideMsg{} })
			}
			return t, slideTick()
		case slideOut:
			t.slideStep++
			if t.slideStep >= SlideFrames {
				t.slideStep = 0
				t.slideState = slideHidden
				t.visible = false
				return t, nil
			}
			return t, slideTick()
		}
		// Stale tick — ignore.
		return t, nil

	case toastHideMsg:
		if !animation.Enabled(t.width) {
			t.visible = false
			t.slideState = slideHidden
			t.slideStep = 0
			return t, nil
		}
		t.slideState = slideOut
		t.slideStep = 0
		return t, slideTick()
	}
	return t, nil
}

func (t ToastModel) View() string {
	if !t.visible || t.message == "" {
		return ""
	}
	body := lipgloss.NewStyle().
		Background(lipgloss.Color("#45475a")).
		Foreground(lipgloss.Color("#cdd6f4")).
		Padding(0, 2).
		Render(t.message)

	if t.slideState == slideShown || !animation.Enabled(t.width) {
		return body
	}

	// Compute leading offset based on slide position. We move the toast
	// "in" from the right by left-padding it with spaces; lipgloss has no
	// negative offset so this is the cheapest visual trick that works.
	pos := t.SlidePos()
	bodyW := lipgloss.Width(body)
	maxOffset := bodyW
	if t.width > 0 && t.width-bodyW > 0 {
		maxOffset = t.width - bodyW
		if maxOffset < 1 {
			maxOffset = 1
		}
	}
	offset := int(float64(maxOffset) * (1.0 - pos))
	if offset <= 0 {
		return body
	}
	return strings.Repeat(" ", offset) + body
}

func slideTick() tea.Cmd {
	return tea.Tick(slideTickInterval, func(time.Time) tea.Msg { return toastSlideTickMsg{} })
}

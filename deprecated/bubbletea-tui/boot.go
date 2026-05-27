package tui

import (
	"image/color"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/Sahaj-Tech-ltd/overkill/deprecated/bubbletea-tui/components/animation"
	"github.com/Sahaj-Tech-ltd/overkill/deprecated/bubbletea-tui/components/logo"
	"github.com/Sahaj-Tech-ltd/overkill/deprecated/bubbletea-tui/theme"
	"github.com/Sahaj-Tech-ltd/overkill/internal/personality"
)

// Boot animation timing.
//
// Total visual sequence under 1.2s so the auto-dismiss never preempts a
// frame mid-animation.
//
//	0..600ms   logo fade-in (BootFadeFrames frames)
//	360..760ms typewriter (subtitle, ~20ms/char)
//	1140ms+    blink "press any key to continue"
//
// At 30 FPS the fade is 18 frames; at 50ms/tick it is 12 frames. We use
// 12 frames at 50ms = 600ms — far cheaper than 30 FPS for SSH.
const (
	BootFadeFrames    = 12
	bootFadeInterval  = 50 * time.Millisecond
	bootTypeInterval  = 20 * time.Millisecond
	bootBlinkFrames   = 2
	bootBlinkInterval = 80 * time.Millisecond
)

// bootFadeTickMsg drives the logo fade.
type bootFadeTickMsg struct{}

// bootTypeTickMsg drives the subtitle typewriter.
type bootTypeTickMsg struct{}

// bootBlinkTickMsg drives the dismiss-hint blink.
type bootBlinkTickMsg struct{}

// BootModel renders the splash screen shown on startup. It is dismissed on
// the first keystroke or after a 2-second auto-dismiss timer; the keystroke
// is forwarded to the editor so the first typed character isn't lost.
type BootModel struct {
	soulMD  string
	funFact string
	visible bool
	ready   bool
	width   int
	height  int
	person  *personality.Personality

	// Animation state
	fadeStep    int  // 0..BootFadeFrames
	typedChars  int  // 0..len(subtitle)
	blinkOn     bool // current blink state
	blinkFrames int  // number of blink toggles done
	logoModel   logo.LogoModel
}

type BootCompleteMsg struct {
	FunFact string
	SoulMD  string
}

func LoadBootData(person *personality.Personality) tea.Cmd {
	return func() tea.Msg {
		msg := BootCompleteMsg{}

		soulPath := filepath.Join(os.Getenv("HOME"), ".overkill", "memories", "soul.md")
		data, err := os.ReadFile(soulPath)
		if err == nil {
			msg.SoulMD = string(data)
		}

		if person != nil {
			msg.FunFact = person.FunFacts().Random()
		}
		// No hardcoded fallback. If no real fact is available we simply omit it.

		return msg
	}
}

// StartBootAnimation kicks off the fade-in. The caller is responsible for
// forwarding bootFadeTickMsg / bootTypeTickMsg / bootBlinkTickMsg back into
// the model via Update. Returns nil when animations are gated off.
func (b *BootModel) StartBootAnimation() tea.Cmd {
	if !animation.Enabled(b.width) {
		// Fast-forward to the final visual state.
		b.fadeStep = BootFadeFrames
		b.typedChars = len(logo.Subtitle)
		b.blinkOn = true
		return nil
	}
	b.fadeStep = 0
	b.typedChars = 0
	b.blinkOn = false
	b.blinkFrames = 0
	return tea.Batch(bootFadeTick(), bootTypeTick(), bootBlinkTick())
}

// StopBootAnimation halts all in-flight boot ticks. Pending messages that
// arrive after Stop are dropped because we mark the model not visible.
func (b *BootModel) StopBootAnimation() {
	b.fadeStep = BootFadeFrames
	b.typedChars = len(logo.Subtitle)
	b.logoModel.Stop()
}

// UpdateBoot routes the boot-tick messages. It returns the new model and
// any follow-up command. Unknown messages are passed through unchanged.
func (b BootModel) UpdateBoot(msg tea.Msg) (BootModel, tea.Cmd) {
	if !b.visible {
		return b, nil
	}
	switch msg.(type) {
	case bootFadeTickMsg:
		if !animation.Enabled(b.width) {
			b.fadeStep = BootFadeFrames
			return b, nil
		}
		if b.fadeStep < BootFadeFrames {
			b.fadeStep++
			if b.fadeStep == BootFadeFrames {
				// Fade complete — start logo shimmer.
				b.logoModel.SetWidth(b.width)
				cmd := b.logoModel.Start()
				return b, cmd
			}
			return b, bootFadeTick()
		}
		return b, nil

	case bootTypeTickMsg:
		// Typewriter starts at 60% of fade (~360ms in). Wait if we're early.
		if b.fadeStep*10 < BootFadeFrames*6 {
			return b, bootTypeTick()
		}
		if b.typedChars < len(logo.Subtitle) {
			b.typedChars++
			return b, bootTypeTick()
		}
		return b, nil

	case bootBlinkTickMsg:
		// Blink starts at 95% of total visual sequence (after typewriter).
		if b.typedChars < len(logo.Subtitle) {
			return b, bootBlinkTick()
		}
		b.blinkOn = !b.blinkOn
		b.blinkFrames++
		if b.blinkFrames < bootBlinkFrames*2 {
			return b, bootBlinkTick()
		}
		// Settle to "on" so the hint doesn't strand mid-blink.
		b.blinkOn = true
		return b, nil

	case logo.ShimmerTickMsg:
		var cmd tea.Cmd
		b.logoModel, cmd = b.logoModel.Update(msg)
		return b, cmd
	}
	return b, nil
}

func (b BootModel) View() string {
	if !b.visible {
		return ""
	}

	width := b.width
	height := b.height
	if width <= 0 {
		width = 80
	}
	if height <= 0 {
		height = 24
	}

	t := theme.CurrentTheme()

	var logoBlock string
	switch {
	case !animation.Enabled(width):
		logoBlock = logo.Render(t)
	case b.fadeStep < BootFadeFrames:
		logoBlock = renderLogoFade(t, b.fadeStep)
	default:
		logoBlock = b.logoModel.View()
		if logoBlock == "" {
			logoBlock = logo.Render(t)
		}
	}

	subtitleStyle := lipgloss.NewStyle().Foreground(t.TextMuted()).Italic(true)
	subText := logo.Subtitle
	if animation.Enabled(width) && b.typedChars < len(subText) {
		subText = subText[:b.typedChars]
	}
	subtitle := subtitleStyle.Render(subText)

	hintStyle := lipgloss.NewStyle().Foreground(t.TextMuted())
	var hint string
	hintReady := !animation.Enabled(width) || b.typedChars >= len(logo.Subtitle)
	if hintReady && (!animation.Enabled(width) || b.blinkOn || b.blinkFrames >= bootBlinkFrames*2) {
		hint = hintStyle.Render("press any key to continue")
	} else {
		// Reserve the row so the layout doesn't shift.
		hint = hintStyle.Render(strings.Repeat(" ", len("press any key to continue")))
	}

	parts := []string{logoBlock, "", subtitle}

	if b.funFact != "" {
		factStyle := lipgloss.NewStyle().Foreground(t.TextMuted())
		parts = append(parts, "", factStyle.Render(b.funFact))
	}

	parts = append(parts, "", hint)

	body := strings.Join(parts, "\n")

	return lipgloss.Place(
		width, height,
		lipgloss.Center, lipgloss.Center,
		body,
	)
}

// renderLogoFade renders the logo dimmed toward TextDim()-equivalent at
// step 0 and full Primary() at step BootFadeFrames. We approximate
// TextDim by blending Text() halfway toward Background().
func renderLogoFade(t theme.Theme, step int) string {
	if step >= BootFadeFrames {
		return logo.Render(t)
	}
	if step < 0 {
		step = 0
	}
	ratio := float64(step) / float64(BootFadeFrames)

	dim := blendBootColors(t.Background(), t.Text(), 0.5)
	primary := t.Primary()
	accent := t.Accent()

	// The static logo already has color codes baked in — we re-render from
	// the raw rows so we can pick the brightness per step.
	raws := bootLogoRows()
	out := make([]string, 0, len(raws))
	for _, raw := range raws {
		runes := []rune(raw)
		mid := len(runes) / 2
		for mid < len(runes) && runes[mid] != ' ' {
			mid++
		}
		left := blendBootColors(dim, primary, ratio)
		right := blendBootColors(dim, accent, ratio)
		leftStyled := lipgloss.NewStyle().Foreground(left).Bold(true).Render(string(runes[:mid]))
		rightStyled := lipgloss.NewStyle().Foreground(right).Bold(true).Render(string(runes[mid:]))
		out = append(out, leftStyled+rightStyled)
	}
	return strings.Join(out, "\n")
}

// bootLogoRows returns the raw glyph rows of the logo. Defined as a thin
// wrapper so we can render at arbitrary brightness without re-parsing
// styled output.
func bootLogoRows() []string {
	return []string{
		"█▀▀█ █  █ █▀▀▀ █▀▀▀ █  █ █ █  █ █",
		"█  █ █  █ █▀▀  █▀▀  █▀▀█ █ █  █ █",
		"█  █  ██  █▀▀▀ █▀▀▀ █  █ █ █▀▀█ █",
		"▀▀▀▀  ▀▀  ▀▀▀▀ ▀▀▀▀ ▀  ▀ ▀ ▀  ▀ ▀",
	}
}

func blendBootColors(a, b lipgloss.Color, ratio float64) lipgloss.Color {
	if ratio <= 0 {
		return a
	}
	if ratio >= 1 {
		return b
	}
	ar, ag, ab, ok1 := bootHexToRGB(string(a))
	br, bg, bb, ok2 := bootHexToRGB(string(b))
	if !ok1 || !ok2 {
		return b
	}
	mix := func(x, y uint8) uint8 {
		return uint8(float64(x)*(1-ratio) + float64(y)*ratio)
	}
	c := color.RGBA{R: mix(ar, br), G: mix(ag, bg), B: mix(ab, bb), A: 0xff}
	return lipgloss.Color(bootRGBToHex(c))
}

func bootHexToRGB(s string) (uint8, uint8, uint8, bool) {
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

func bootRGBToHex(c color.RGBA) string {
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

func bootFadeTick() tea.Cmd {
	return tea.Tick(bootFadeInterval, func(time.Time) tea.Msg { return bootFadeTickMsg{} })
}

func bootTypeTick() tea.Cmd {
	return tea.Tick(bootTypeInterval, func(time.Time) tea.Msg { return bootTypeTickMsg{} })
}

func bootBlinkTick() tea.Cmd {
	return tea.Tick(bootBlinkInterval, func(time.Time) tea.Msg { return bootBlinkTickMsg{} })
}

package logo

import (
	"image/color"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/Sahaj-Tech-ltd/ethos/pkg/tui/theme"
)

// ShimmerFrames is the number of discrete frames in one shimmer cycle.
// At ~8 FPS this is ~3.5 seconds per pass, matching opencode's pacing.
const ShimmerFrames = 28

// brightnessCurve maps "distance behind the wave" → blend ratio toward
// the peak primary color. Index 0 is the wavefront itself (full primary),
// index N tails off toward 0 (pure base text). Anything ahead of the wave
// is just base text.
var brightnessCurve = buildBrightnessCurve()

func buildBrightnessCurve() []float64 {
	const tailLen = 12
	curve := make([]float64, tailLen)
	for i := 0; i < tailLen; i++ {
		// Smooth exponential tail so the head is bright and falls off fast.
		t := float64(i) / float64(tailLen-1)
		curve[i] = (1.0 - t) * (1.0 - t)
	}
	return curve
}

// RenderShimmer returns the colored logo at the given animation frame.
// frame is wrapped to ShimmerFrames so callers do not have to worry about
// overflow. When t is nil, the current theme is used.
func RenderShimmer(t theme.Theme, frame int) string {
	if t == nil {
		t = theme.CurrentTheme()
	}
	if ShimmerFrames <= 0 {
		return Render(t)
	}

	frame = ((frame % ShimmerFrames) + ShimmerFrames) % ShimmerFrames

	// Wavefront column for this frame: sweep across rendered width plus tail
	// so the trailing fade exits the right edge cleanly.
	width := Width()
	totalSpan := width + len(brightnessCurve)
	wavePos := (frame * totalSpan) / ShimmerFrames

	base := t.Text()
	peak := t.Primary()
	accent := t.Accent()

	out := make([]string, 0, len(logoRows))
	for _, row := range logoRows {
		runes := []rune(row)
		mid := len(runes) / 2
		for mid < len(runes) && runes[mid] != ' ' {
			mid++
		}

		var sb strings.Builder
		for i, r := range runes {
			if r == ' ' {
				sb.WriteRune(r)
				continue
			}
			// Pick the slot's "natural" color (left=primary, right=accent).
			natural := peak
			if i >= mid {
				natural = accent
			}

			// distance behind the wavefront (positive = trailing)
			d := wavePos - i
			intensity := 0.0
			if d >= 0 && d < len(brightnessCurve) {
				intensity = brightnessCurve[d]
			}

			fg := blendColors(base, natural, intensity)
			styled := lipgloss.NewStyle().
				Foreground(fg).
				Bold(true).
				Render(string(r))
			sb.WriteString(styled)
		}
		out = append(out, sb.String())
	}
	return strings.Join(out, "\n")
}

// blendColors mixes two lipgloss colors by ratio (0=a, 1=b). Both inputs
// must be hex strings (#rrggbb); anything else falls back to b.
func blendColors(a, b lipgloss.Color, ratio float64) lipgloss.Color {
	if ratio <= 0 {
		return a
	}
	if ratio >= 1 {
		return b
	}
	ar, ag, ab, ok1 := hexToRGB(string(a))
	br, bg, bb, ok2 := hexToRGB(string(b))
	if !ok1 || !ok2 {
		return b
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
		// #rgb shorthand
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

// BrightnessCurveCopy returns a copy of the precomputed brightness curve;
// exposed only for tests.
func BrightnessCurveCopy() []float64 {
	out := make([]float64, len(brightnessCurve))
	copy(out, brightnessCurve)
	return out
}

package cellrender

import (
	"unicode/utf8"
)

// Parse walks the ANSI bytes in `s` and writes printable runes into a buffer
// of the given width and height. The cursor starts at (0,0). Newlines move
// to the next row, column 0. Recognized escape sequences:
//
//   - CSI <n>;<m> H or f  cursor position (1-based)
//   - CSI <n> A/B/C/D     cursor up/down/forward/back
//   - CSI <n> G           cursor horizontal absolute
//   - CSI <n> J           erase in display (0=cur→end, 1=start→cur, 2=all)
//   - CSI <n> K           erase in line   (0=cur→end, 1=start→cur, 2=line)
//   - CSI <params> m      SGR (colors + attributes; 16/256/RGB)
//   - ESC 7 / ESC 8       save/restore cursor (best-effort)
//
// Unknown sequences are skipped (their bytes are consumed and dropped).
// This is intentionally narrower than a full VT500 emulator — it covers what
// Bubble Tea + lipgloss actually emit.
func Parse(s string, width, height int) *Buffer {
	b := NewBuffer(width, height)
	ApplyTo(b, s)
	return b
}

// ApplyTo runs the same parse onto an existing buffer, starting from cursor
// (0,0) and current default style. Useful for incremental scenarios.
func ApplyTo(b *Buffer, s string) {
	p := &parserState{
		buf:   b,
		style: Cell{Rune: ' '},
	}
	p.run(s)
}

type parserState struct {
	buf      *Buffer
	cx, cy   int
	saveX    int
	saveY    int
	style    Cell // FG/BG/Attr currently active
}

func (p *parserState) run(s string) {
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == 0x1b: // ESC
			n := p.parseEscape(s[i:])
			if n <= 0 {
				i++
			} else {
				i += n
			}
		case c == '\n':
			p.cy++
			p.cx = 0
			i++
		case c == '\r':
			p.cx = 0
			i++
		case c == '\t':
			// Round to next 8-cell tab stop, capped at right margin.
			p.cx = ((p.cx / 8) + 1) * 8
			if p.cx >= p.buf.W {
				p.cx = p.buf.W - 1
			}
			i++
		case c == 0x08: // backspace
			if p.cx > 0 {
				p.cx--
			}
			i++
		case c < 0x20:
			// Other C0 control: drop silently.
			i++
		default:
			r, size := utf8.DecodeRuneInString(s[i:])
			if r == utf8.RuneError && size == 1 {
				i++
				continue
			}
			p.writeRune(r)
			i += size
		}
	}
}

func (p *parserState) writeRune(r rune) {
	if p.cy < 0 || p.cy >= p.buf.H {
		// Off-screen rows still advance the cursor; we just drop the write.
		p.cx++
		return
	}
	// If a previous write left the cursor "hanging" past the right margin,
	// wrap now (deferred-wrap, matching xterm behavior).
	if p.cx >= p.buf.W {
		p.cx = 0
		p.cy++
		if p.cy >= p.buf.H {
			return
		}
	}
	cell := p.style
	cell.Rune = r
	p.buf.Set(p.cx, p.cy, cell)
	p.cx++
}

// parseEscape returns the number of bytes consumed (including the leading
// 0x1b). 0 means "not enough data" — caller advances by 1.
func (p *parserState) parseEscape(s string) int {
	if len(s) < 2 {
		return 0
	}
	switch s[1] {
	case '[':
		return p.parseCSI(s)
	case ']':
		return p.parseOSC(s)
	case '7':
		p.saveX, p.saveY = p.cx, p.cy
		return 2
	case '8':
		p.cx, p.cy = p.saveX, p.saveY
		return 2
	case '=', '>', '(', ')':
		// Application/Numeric keypad, charset selection — consume 2-3 bytes.
		if s[1] == '(' || s[1] == ')' {
			if len(s) >= 3 {
				return 3
			}
			return 0
		}
		return 2
	default:
		// Unknown 2-byte escape: drop both bytes.
		return 2
	}
}

func (p *parserState) parseCSI(s string) int {
	// s[0]=ESC, s[1]='['. Scan params (digits, ';', '?', '>'), then final byte.
	i := 2
	paramStart := i
	for i < len(s) {
		c := s[i]
		if (c >= '0' && c <= '9') || c == ';' || c == '?' || c == '>' || c == '!' {
			i++
			continue
		}
		break
	}
	if i >= len(s) {
		return 0
	}
	final := s[i]
	params := s[paramStart:i]
	i++ // consume final
	p.dispatchCSI(params, final)
	return i
}

func (p *parserState) parseOSC(s string) int {
	// ESC ] ... (BEL or ESC \). Just skip — OSC affects window title, hyperlinks,
	// and color queries; none of these affect cell content.
	for i := 2; i < len(s); i++ {
		if s[i] == 0x07 {
			return i + 1
		}
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '\\' {
			return i + 2
		}
	}
	return len(s)
}

// dispatchCSI applies the CSI command. params is the raw parameter string
// (without prefixes like '?'); final is the command letter.
func (p *parserState) dispatchCSI(params string, final byte) {
	// Private-use prefix (?, >, !) — ignore the command. Most are mode set/reset
	// (cursor visibility, alt-screen, mouse) which don't affect cell contents.
	if len(params) > 0 {
		switch params[0] {
		case '?', '>', '!':
			return
		}
	}
	nums := parseParams(params)
	get := func(idx, def int) int {
		if idx < len(nums) && nums[idx] != -1 {
			return nums[idx]
		}
		return def
	}
	switch final {
	case 'H', 'f':
		row := get(0, 1)
		col := get(1, 1)
		p.cy = row - 1
		p.cx = col - 1
		clampCursor(p)
	case 'A':
		p.cy -= get(0, 1)
	case 'B':
		p.cy += get(0, 1)
	case 'C':
		p.cx += get(0, 1)
	case 'D':
		p.cx -= get(0, 1)
	case 'E':
		p.cy += get(0, 1)
		p.cx = 0
	case 'F':
		p.cy -= get(0, 1)
		p.cx = 0
	case 'G':
		p.cx = get(0, 1) - 1
	case 'd':
		p.cy = get(0, 1) - 1
	case 'J':
		p.eraseDisplay(get(0, 0))
	case 'K':
		p.eraseLine(get(0, 0))
	case 'm':
		p.applySGR(nums)
	case 's':
		p.saveX, p.saveY = p.cx, p.cy
	case 'u':
		p.cx, p.cy = p.saveX, p.saveY
	}
	clampCursor(p)
}

func clampCursor(p *parserState) {
	if p.cx < 0 {
		p.cx = 0
	}
	if p.cy < 0 {
		p.cy = 0
	}
	if p.cx >= p.buf.W {
		p.cx = p.buf.W - 1
	}
	if p.cy >= p.buf.H {
		p.cy = p.buf.H - 1
	}
}

func (p *parserState) eraseDisplay(mode int) {
	switch mode {
	case 0:
		// Cursor to end of screen.
		p.eraseLine(0)
		for y := p.cy + 1; y < p.buf.H; y++ {
			for x := 0; x < p.buf.W; x++ {
				p.buf.Set(x, y, blankCell)
			}
		}
	case 1:
		// Start to cursor.
		for y := 0; y < p.cy; y++ {
			for x := 0; x < p.buf.W; x++ {
				p.buf.Set(x, y, blankCell)
			}
		}
		p.eraseLine(1)
	case 2, 3:
		p.buf.Clear()
	}
}

func (p *parserState) eraseLine(mode int) {
	switch mode {
	case 0:
		for x := p.cx; x < p.buf.W; x++ {
			p.buf.Set(x, p.cy, blankCell)
		}
	case 1:
		for x := 0; x <= p.cx && x < p.buf.W; x++ {
			p.buf.Set(x, p.cy, blankCell)
		}
	case 2:
		for x := 0; x < p.buf.W; x++ {
			p.buf.Set(x, p.cy, blankCell)
		}
	}
}

// parseParams converts "1;38;2;255;0;0" → [1, 38, 2, 255, 0, 0].
// Empty parameter slots become -1 (treated as "use default") downstream.
func parseParams(s string) []int {
	if s == "" {
		return nil
	}
	out := make([]int, 0, 8)
	cur := -1
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= '0' && c <= '9' {
			if cur < 0 {
				cur = 0
			}
			cur = cur*10 + int(c-'0')
		} else if c == ';' {
			out = append(out, cur)
			cur = -1
		}
	}
	out = append(out, cur)
	return out
}

func (p *parserState) applySGR(nums []int) {
	if len(nums) == 0 {
		// Bare CSI m == reset.
		p.style = Cell{Rune: ' '}
		return
	}
	for i := 0; i < len(nums); i++ {
		n := nums[i]
		if n == -1 {
			n = 0
		}
		switch {
		case n == 0:
			p.style.FG = DefaultColor
			p.style.BG = DefaultColor
			p.style.Attr = 0
		case n == 1:
			p.style.Attr |= AttrBold
		case n == 2:
			p.style.Attr |= AttrFaint
		case n == 3:
			p.style.Attr |= AttrItalic
		case n == 4:
			p.style.Attr |= AttrUnderline
		case n == 5, n == 6:
			p.style.Attr |= AttrBlink
		case n == 7:
			p.style.Attr |= AttrReverse
		case n == 9:
			p.style.Attr |= AttrStrike
		case n == 22:
			p.style.Attr &^= AttrBold | AttrFaint
		case n == 23:
			p.style.Attr &^= AttrItalic
		case n == 24:
			p.style.Attr &^= AttrUnderline
		case n == 25:
			p.style.Attr &^= AttrBlink
		case n == 27:
			p.style.Attr &^= AttrReverse
		case n == 29:
			p.style.Attr &^= AttrStrike
		case n >= 30 && n <= 37:
			p.style.FG = Color{Mode: 1, Value: uint32(n - 30)}
		case n == 38:
			c, consumed := parseExtColor(nums[i+1:])
			i += consumed
			p.style.FG = c
		case n == 39:
			p.style.FG = DefaultColor
		case n >= 40 && n <= 47:
			p.style.BG = Color{Mode: 1, Value: uint32(n - 40)}
		case n == 48:
			c, consumed := parseExtColor(nums[i+1:])
			i += consumed
			p.style.BG = c
		case n == 49:
			p.style.BG = DefaultColor
		case n >= 90 && n <= 97:
			p.style.FG = Color{Mode: 1, Value: uint32(n - 90 + 8)}
		case n >= 100 && n <= 107:
			p.style.BG = Color{Mode: 1, Value: uint32(n - 100 + 8)}
		}
	}
}

// parseExtColor handles the params after 38/48: either "5;<idx>" (256-color)
// or "2;<r>;<g>;<b>" (truecolor). Returns the color and how many params were
// consumed past the 38/48.
func parseExtColor(rest []int) (Color, int) {
	if len(rest) == 0 {
		return DefaultColor, 0
	}
	switch rest[0] {
	case 5:
		if len(rest) >= 2 {
			return Color{Mode: 2, Value: uint32(rest[1] & 0xff)}, 2
		}
		return DefaultColor, 1
	case 2:
		if len(rest) >= 4 {
			r := uint32(rest[1] & 0xff)
			g := uint32(rest[2] & 0xff)
			b := uint32(rest[3] & 0xff)
			return Color{Mode: 3, Value: (r << 16) | (g << 8) | b}, 4
		}
		return DefaultColor, len(rest)
	}
	return DefaultColor, 0
}

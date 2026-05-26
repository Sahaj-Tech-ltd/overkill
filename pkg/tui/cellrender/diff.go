package cellrender

import (
	"strconv"
	"strings"
)

// Diff walks prev and curr cell-by-cell and returns the minimal byte sequence
// that, when written to a terminal currently showing prev, will bring it into
// sync with curr.
//
// The emitter coalesces:
//   - Cursor moves: only emitted when the next-to-write cell is not where the
//     terminal cursor would naturally be after the previous write.
//   - SGR changes: only emitted when FG/BG/Attr differ from the last written
//     cell.
//
// If prev is nil or has different dimensions than curr, the result is a full
// repaint of curr (preceded by a clear-screen).
func Diff(prev, curr *Buffer) []byte {
	if curr == nil {
		return nil
	}
	if prev == nil || prev.W != curr.W || prev.H != curr.H {
		return fullRepaint(curr)
	}

	var out strings.Builder
	out.Grow(64)

	// Cursor position the terminal would have after our last emitted action.
	// -1,-1 means "unknown — emit absolute position before next rune".
	cx, cy := -1, -1
	curStyle := Cell{Rune: ' '} // FG/BG/Attr currently active in the terminal

	for y := 0; y < curr.H; y++ {
		for x := 0; x < curr.W; x++ {
			pc := prev.Get(x, y)
			cc := curr.Get(x, y)
			if pc.Equal(cc) {
				continue
			}
			// Reposition if needed.
			if cx != x || cy != y {
				writeCUP(&out, x, y)
				cx, cy = x, y
			}
			// Style change?
			if cc.FG != curStyle.FG || cc.BG != curStyle.BG || cc.Attr != curStyle.Attr {
				writeSGR(&out, curStyle, cc)
				curStyle.FG, curStyle.BG, curStyle.Attr = cc.FG, cc.BG, cc.Attr
			}
			r := cc.Rune
			if r == 0 {
				r = ' '
			}
			out.WriteRune(r)
			cx++
			if cx >= curr.W {
				// Cursor wrap is terminal-specific; force a fresh CUP next cell.
				cx, cy = -1, -1
			}
		}
	}
	if out.Len() == 0 {
		return nil
	}
	// Reset SGR at end so subsequent unrelated writes don't inherit our style.
	if curStyle.FG != DefaultColor || curStyle.BG != DefaultColor || curStyle.Attr != 0 {
		out.WriteString("\x1b[0m")
	}
	return []byte(out.String())
}

func fullRepaint(b *Buffer) []byte {
	var out strings.Builder
	out.Grow(b.W * b.H)
	out.WriteString("\x1b[2J\x1b[H") // clear + home
	style := Cell{Rune: ' '}
	for y := 0; y < b.H; y++ {
		writeCUP(&out, 0, y)
		for x := 0; x < b.W; x++ {
			c := b.Get(x, y)
			if c.FG != style.FG || c.BG != style.BG || c.Attr != style.Attr {
				writeSGR(&out, style, c)
				style.FG, style.BG, style.Attr = c.FG, c.BG, c.Attr
			}
			r := c.Rune
			if r == 0 {
				r = ' '
			}
			out.WriteRune(r)
		}
	}
	out.WriteString("\x1b[0m")
	return []byte(out.String())
}

// writeCUP writes a 1-based cursor-position escape for the given 0-based (x,y).
func writeCUP(out *strings.Builder, x, y int) {
	out.WriteString("\x1b[")
	out.WriteString(strconv.Itoa(y + 1))
	out.WriteByte(';')
	out.WriteString(strconv.Itoa(x + 1))
	out.WriteByte('H')
}

// writeSGR emits the minimal SGR delta to transition from `from` to `to`.
//
// Strategy: if any attribute bit was cleared, we have to reset and re-apply
// everything (terminals have no "clear single attribute" that's universally
// supported beyond 22/23/24/25/27/29; we do use those when only adds happen).
func writeSGR(out *strings.Builder, from, to Cell) {
	// If colors got reset to default OR attrs lost any bit, emit a full reset
	// and rebuild. This is correct and only marginally larger than per-bit
	// resets in the common case.
	addedAttrs := to.Attr &^ from.Attr
	removedAttrs := from.Attr &^ to.Attr
	colorsChanged := !to.FG.Equal(from.FG) || !to.BG.Equal(from.BG)

	parts := make([]string, 0, 8)
	if removedAttrs != 0 || (from.FG != DefaultColor && to.FG == DefaultColor) || (from.BG != DefaultColor && to.BG == DefaultColor) {
		parts = append(parts, "0")
		// After reset we must re-emit everything still active.
		for _, a := range allAttrCodes(to.Attr) {
			parts = append(parts, a)
		}
		if to.FG != DefaultColor {
			parts = append(parts, fgCodes(to.FG)...)
		}
		if to.BG != DefaultColor {
			parts = append(parts, bgCodes(to.BG)...)
		}
	} else {
		for _, a := range allAttrCodes(addedAttrs) {
			parts = append(parts, a)
		}
		if colorsChanged {
			if !to.FG.Equal(from.FG) && to.FG != DefaultColor {
				parts = append(parts, fgCodes(to.FG)...)
			}
			if !to.BG.Equal(from.BG) && to.BG != DefaultColor {
				parts = append(parts, bgCodes(to.BG)...)
			}
		}
	}
	if len(parts) == 0 {
		return
	}
	out.WriteString("\x1b[")
	out.WriteString(strings.Join(parts, ";"))
	out.WriteByte('m')
}

func allAttrCodes(a Attr) []string {
	out := []string{}
	if a&AttrBold != 0 {
		out = append(out, "1")
	}
	if a&AttrFaint != 0 {
		out = append(out, "2")
	}
	if a&AttrItalic != 0 {
		out = append(out, "3")
	}
	if a&AttrUnderline != 0 {
		out = append(out, "4")
	}
	if a&AttrBlink != 0 {
		out = append(out, "5")
	}
	if a&AttrReverse != 0 {
		out = append(out, "7")
	}
	if a&AttrStrike != 0 {
		out = append(out, "9")
	}
	return out
}

func fgCodes(c Color) []string {
	switch c.Mode {
	case 1:
		v := int(c.Value)
		if v < 8 {
			return []string{strconv.Itoa(30 + v)}
		}
		if v < 16 {
			return []string{strconv.Itoa(90 + v - 8)}
		}
	case 2:
		return []string{"38", "5", strconv.Itoa(int(c.Value))}
	case 3:
		r := int((c.Value >> 16) & 0xff)
		g := int((c.Value >> 8) & 0xff)
		b := int(c.Value & 0xff)
		return []string{"38", "2", strconv.Itoa(r), strconv.Itoa(g), strconv.Itoa(b)}
	}
	return nil
}

func bgCodes(c Color) []string {
	switch c.Mode {
	case 1:
		v := int(c.Value)
		if v < 8 {
			return []string{strconv.Itoa(40 + v)}
		}
		if v < 16 {
			return []string{strconv.Itoa(100 + v - 8)}
		}
	case 2:
		return []string{"48", "5", strconv.Itoa(int(c.Value))}
	case 3:
		r := int((c.Value >> 16) & 0xff)
		g := int((c.Value >> 8) & 0xff)
		b := int(c.Value & 0xff)
		return []string{"48", "2", strconv.Itoa(r), strconv.Itoa(g), strconv.Itoa(b)}
	}
	return nil
}

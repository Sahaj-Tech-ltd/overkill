// Package cellrender implements a cell-level diff renderer for Bubble Tea
// programs. It opt-in wraps the program's output writer, parses each emitted
// frame into a Buffer of Cells, diffs against the previous frame, and emits
// only the minimal terminal escape sequences needed to bring the visible
// terminal in sync with the new frame.
//
// This mirrors what opentui's native renderer does and avoids the per-line
// repaint cost of Bubble Tea v1's lipgloss-based renderer.
package cellrender

// Attr is a packed bitset of SGR text attributes.
type Attr uint16

const (
	AttrBold Attr = 1 << iota
	AttrFaint
	AttrItalic
	AttrUnderline
	AttrBlink
	AttrReverse
	AttrStrike
)

// Color represents a foreground or background color in a normalized form.
//
// Mode encodes how Value should be interpreted:
//   - 0: default (terminal default; Value ignored)
//   - 1: 16-color ANSI (Value 0..15)
//   - 2: 256-color (Value 0..255)
//   - 3: truecolor RGB (Value packed as 0xRRGGBB)
type Color struct {
	Mode  uint8
	Value uint32
}

// DefaultColor is the sentinel "use terminal default" color.
var DefaultColor = Color{}

// Equal reports whether two colors are byte-identical.
func (c Color) Equal(o Color) bool { return c.Mode == o.Mode && c.Value == o.Value }

// Cell is one terminal cell: the visible rune plus its visual styling.
//
// Wide runes (East Asian width=2) occupy two cells; the trailing cell holds
// rune == 0 and is treated as "owned by the previous cell" by the diff
// emitter.
type Cell struct {
	Rune rune
	FG   Color
	BG   Color
	Attr Attr
}

// blankCell is the implicit value for any cell never written. We treat space
// with default colors as the empty cell so newly-allocated buffers compare
// equal to a freshly-cleared screen.
var blankCell = Cell{Rune: ' '}

// Equal reports whether two cells are visually identical.
func (c Cell) Equal(o Cell) bool {
	return c.Rune == o.Rune && c.FG.Equal(o.FG) && c.BG.Equal(o.BG) && c.Attr == o.Attr
}

// Buffer is a fixed-size grid of cells laid out in row-major order.
type Buffer struct {
	W, H  int
	cells []Cell
}

// NewBuffer allocates a buffer of (w x h) blank cells. Panics on
// non-positive dimensions to surface programmer errors immediately.
func NewBuffer(w, h int) *Buffer {
	if w <= 0 || h <= 0 {
		w, h = 1, 1
	}
	cells := make([]Cell, w*h)
	for i := range cells {
		cells[i] = blankCell
	}
	return &Buffer{W: w, H: h, cells: cells}
}

// inBounds is the single guard for all (x,y) accessors.
func (b *Buffer) inBounds(x, y int) bool {
	return x >= 0 && y >= 0 && x < b.W && y < b.H
}

// Get returns the cell at (x,y) or a blank cell if out of bounds.
func (b *Buffer) Get(x, y int) Cell {
	if !b.inBounds(x, y) {
		return blankCell
	}
	return b.cells[y*b.W+x]
}

// Set writes c to (x,y); out-of-bounds writes are silently ignored, which
// matches how terminals clip output past the right margin.
func (b *Buffer) Set(x, y int, c Cell) {
	if !b.inBounds(x, y) {
		return
	}
	b.cells[y*b.W+x] = c
}

// Clear resets every cell to blank. Used on \x1b[2J or full repaint signals.
func (b *Buffer) Clear() {
	for i := range b.cells {
		b.cells[i] = blankCell
	}
}

// Equal reports whether two buffers have identical dimensions and contents.
func (b *Buffer) Equal(o *Buffer) bool {
	if b == nil || o == nil || b.W != o.W || b.H != o.H {
		return false
	}
	for i := range b.cells {
		if !b.cells[i].Equal(o.cells[i]) {
			return false
		}
	}
	return true
}

// Resize reallocates the buffer at the new dimensions, preserving any cells
// that fall inside the new region (anchored at the top-left).
func (b *Buffer) Resize(w, h int) {
	if w == b.W && h == b.H {
		return
	}
	if w <= 0 || h <= 0 {
		w, h = 1, 1
	}
	next := make([]Cell, w*h)
	for i := range next {
		next[i] = blankCell
	}
	copyW := w
	if b.W < copyW {
		copyW = b.W
	}
	copyH := h
	if b.H < copyH {
		copyH = b.H
	}
	for y := 0; y < copyH; y++ {
		for x := 0; x < copyW; x++ {
			next[y*w+x] = b.cells[y*b.W+x]
		}
	}
	b.W, b.H, b.cells = w, h, next
}

// Clone returns a deep copy of the buffer.
func (b *Buffer) Clone() *Buffer {
	c := &Buffer{W: b.W, H: b.H, cells: make([]Cell, len(b.cells))}
	copy(c.cells, b.cells)
	return c
}

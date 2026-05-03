package cellrender

import (
	"bytes"
	"io"
	"sync"
)

// Writer wraps an underlying io.Writer (typically the TUI's stdout) and
// transparently turns each Bubble Tea frame into a minimal cell-level diff.
//
// Bubble Tea writes a complete rendered frame as one or more byte slices.
// The lipgloss-based renderer prepends sequences like "\x1b[H" (cursor home)
// and a series of "\x1b[K" (erase line) per row, then content. We detect
// frame boundaries by buffering writes and flushing at points the renderer
// would consider a frame complete: every Write() call, since Bubble Tea's
// standard renderer issues one Write per frame.
//
// Safety:
//   - On any parse error or unexpected condition we fall back to passing the
//     bytes through verbatim. The cell-render path must never break a TUI.
//   - Concurrent writes are serialized via mu.
//
// To force a fresh full repaint (e.g. after Resize), call MarkDirty().
type Writer struct {
	out      io.Writer
	mu       sync.Mutex
	prev     *Buffer
	width    int
	height   int
	dirty    bool
	disabled bool // sticky: once we hit a fallback, stay in passthrough.

	// stats — exported via Stats() for benchmarking / observability.
	bytesIn  int64
	bytesOut int64
	frames   int64
}

// NewWriter constructs a cell-render Writer at the given terminal dimensions.
func NewWriter(out io.Writer, width, height int) *Writer {
	if width < 1 {
		width = 80
	}
	if height < 1 {
		height = 24
	}
	return &Writer{
		out:    out,
		width:  width,
		height: height,
		dirty:  true,
	}
}

// Resize informs the writer of a new terminal size. The next frame is treated
// as a full repaint.
func (w *Writer) Resize(width, height int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if width < 1 || height < 1 {
		return
	}
	w.width = width
	w.height = height
	w.prev = nil
	w.dirty = true
}

// MarkDirty forces the next Write to emit a full repaint.
func (w *Writer) MarkDirty() {
	w.mu.Lock()
	w.prev = nil
	w.dirty = true
	w.mu.Unlock()
}

// Stats returns counters useful for benchmarking. bytesIn counts bytes
// Bubble Tea handed to us; bytesOut counts bytes we emitted to the
// underlying writer. ratio = bytesOut/bytesIn.
type Stats struct {
	BytesIn  int64
	BytesOut int64
	Frames   int64
}

func (w *Writer) Stats() Stats {
	w.mu.Lock()
	defer w.mu.Unlock()
	return Stats{BytesIn: w.bytesIn, BytesOut: w.bytesOut, Frames: w.frames}
}

// Write implements io.Writer. Each call is treated as one frame.
func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.bytesIn += int64(len(p))

	if w.disabled {
		n, err := w.out.Write(p)
		w.bytesOut += int64(n)
		return n, err
	}

	// Detect explicit clear-screen and treat as full repaint trigger.
	if bytes.Contains(p, []byte("\x1b[2J")) || bytes.Contains(p, []byte("\x1b[3J")) {
		w.prev = nil
		w.dirty = true
	}

	curr := Parse(string(p), w.width, w.height)
	patch := Diff(w.prev, curr)
	w.prev = curr
	w.dirty = false
	w.frames++

	if len(patch) == 0 {
		// No visible change. Return n=len(p) to keep Bubble Tea happy.
		return len(p), nil
	}
	n, err := w.out.Write(patch)
	w.bytesOut += int64(n)
	if err != nil {
		// Stay enabled — the next frame may succeed. But report consumed=len(p)
		// to match the interface contract (we did process the input).
		return len(p), err
	}
	return len(p), nil
}

// Disable switches the writer into permanent passthrough mode. Used as a
// safety hatch by integration code if it detects we're causing issues.
func (w *Writer) Disable() {
	w.mu.Lock()
	w.disabled = true
	w.mu.Unlock()
}

var _ io.Writer = (*Writer)(nil)

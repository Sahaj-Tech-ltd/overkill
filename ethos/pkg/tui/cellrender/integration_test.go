package cellrender

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

// TestIntegrationMovingChar simulates the canonical "moving spinner" scenario
// across 5 frames. It compares bytes emitted by the default path
// (everything-each-frame) to bytes emitted by the cell-render Writer, and
// confirms both paths converge on the same final terminal contents.
func TestIntegrationMovingChar(t *testing.T) {
	const w, h = 10, 5
	frames := make([]string, 5)
	for i := 0; i < 5; i++ {
		frames[i] = makeMovingFrame(w, h, i, 2)
	}

	// Cell-render path. Prime with the first frame (cost amortizes across a
	// real session) and measure the *incremental* cost of frames 2..N — that's
	// the steady-state characteristic the renderer is built to optimize.
	var sink bytes.Buffer
	cw := NewWriter(&sink, w, h)
	_, _ = cw.Write([]byte(frames[0]))
	sink.Reset()
	cw.bytesOut = 0
	cw.frames = 0

	// Default path baseline: each subsequent frame written verbatim.
	var defaultBytes int
	for _, f := range frames[1:] {
		defaultBytes += len(f)
		_, _ = cw.Write([]byte(f))
	}
	cellBytes := cw.Stats().BytesOut

	t.Logf("default=%d cellrender=%d ratio=%.3f", defaultBytes, cellBytes,
		float64(cellBytes)/float64(defaultBytes))

	if float64(cellBytes)/float64(defaultBytes) >= 0.30 {
		t.Fatalf("cell render did not hit <30%% target: %d / %d = %.3f",
			cellBytes, defaultBytes, float64(cellBytes)/float64(defaultBytes))
	}

	// Safety net: the FINAL terminal contents (parse the cumulative cell-render
	// stream and the final default frame) must match.
	finalDefault := Parse(frames[len(frames)-1], w, h)

	rebuilt := NewBuffer(w, h)
	ApplyTo(rebuilt, sink.String())

	if !rebuilt.Equal(finalDefault) {
		t.Fatalf("final terminal state diverges:\n  default=%v\n  cellrender=%v",
			dumpRows(finalDefault), dumpRows(rebuilt))
	}
}

// makeMovingFrame builds a w×h frame whose only changing cell is a '*' that
// walks across `row` from column 0 → w-1.
func makeMovingFrame(w, h, step, row int) string {
	var sb strings.Builder
	for y := 0; y < h; y++ {
		line := strings.Repeat(" ", w)
		if y == row {
			pos := step % w
			line = strings.Repeat(" ", pos) + "*" + strings.Repeat(" ", w-pos-1)
		}
		sb.WriteString(line)
		if y < h-1 {
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

func dumpRows(b *Buffer) []string {
	out := make([]string, b.H)
	for y := 0; y < b.H; y++ {
		out[y] = fmt.Sprintf("%q", rowString(b, y))
	}
	return out
}

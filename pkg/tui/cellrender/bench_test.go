package cellrender

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

// buildFrame constructs an 80x24 frame with N rows of text and a spinner cell
// at (col, row=23). Switching the spinner glyph gives a typical "spinner tick"
// frame-to-frame delta.
func buildFrame(spinner rune) string {
	var sb strings.Builder
	for y := 0; y < 24; y++ {
		if y == 23 {
			sb.WriteString(strings.Repeat(" ", 79))
			sb.WriteRune(spinner)
		} else {
			sb.WriteString(fmt.Sprintf("%-80s", fmt.Sprintf("row %d content", y)))
		}
		if y < 23 {
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

// BenchmarkDefaultBytesPerFrame measures the bytes Bubble Tea would write per
// frame in default mode: the full frame is emitted each time.
func BenchmarkDefaultBytesPerFrame(b *testing.B) {
	frame1 := buildFrame('|')
	frame2 := buildFrame('/')
	b.ReportAllocs()
	var totalBytes int64
	for i := 0; i < b.N; i++ {
		// Default: full frame written every time.
		totalBytes += int64(len(frame1)) + int64(len(frame2))
	}
	b.ReportMetric(float64(totalBytes)/float64(b.N*2), "bytes/frame")
}

// BenchmarkCellRenderBytesPerFrame measures bytes the cell renderer emits per
// frame for the same workload.
func BenchmarkCellRenderBytesPerFrame(b *testing.B) {
	frame1 := buildFrame('|')
	frame2 := buildFrame('/')
	var sink bytes.Buffer
	w := NewWriter(&sink, 80, 24)
	// Prime: emit the first frame so the second is the realistic incremental case.
	_, _ = w.Write([]byte(frame1))
	sink.Reset()
	w.bytesOut = 0
	w.bytesIn = 0
	w.frames = 0

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = w.Write([]byte(frame2))
		_, _ = w.Write([]byte(frame1))
	}
	stats := w.Stats()
	b.ReportMetric(float64(stats.BytesOut)/float64(stats.Frames), "bytes/frame")
}

// TestBytesPerFrameRatio captures the win in a non-bench test so it shows up
// in the standard test run with a printable ratio.
func TestBytesPerFrameRatio(t *testing.T) {
	frame1 := buildFrame('|')
	frame2 := buildFrame('/')

	defaultBytes := len(frame1) + len(frame2)

	var sink bytes.Buffer
	w := NewWriter(&sink, 80, 24)
	_, _ = w.Write([]byte(frame1))
	sink.Reset()
	w.bytesOut = 0
	w.bytesIn = 0
	w.frames = 0
	_, _ = w.Write([]byte(frame2))
	_, _ = w.Write([]byte(frame1))

	cellBytes := w.Stats().BytesOut
	ratio := float64(cellBytes) / float64(defaultBytes)

	t.Logf("default=%d bytes/2frames, cellrender=%d bytes/2frames, ratio=%.4f",
		defaultBytes, cellBytes, ratio)

	if ratio >= 0.5 {
		t.Fatalf("cell-render did not achieve <50%% reduction: ratio=%.4f", ratio)
	}
}

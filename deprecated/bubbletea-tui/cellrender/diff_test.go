package cellrender

import (
	"strings"
	"testing"
)

func TestDiffNoChange(t *testing.T) {
	a := NewBuffer(5, 2)
	b := NewBuffer(5, 2)
	if got := Diff(a, b); len(got) != 0 {
		t.Fatalf("identical buffers should diff to empty, got %q", got)
	}
}

func TestDiffSingleCell(t *testing.T) {
	prev := NewBuffer(5, 2)
	curr := prev.Clone()
	curr.Set(2, 1, Cell{Rune: 'X'})

	out := string(Diff(prev, curr))
	// Expect a CUP to (3,3) (1-based), then 'X'.
	if !strings.Contains(out, "\x1b[2;3H") {
		t.Fatalf("missing CUP, got %q", out)
	}
	if !strings.Contains(out, "X") {
		t.Fatalf("missing rune, got %q", out)
	}
	// Should be tiny — well under 20 bytes.
	if len(out) > 20 {
		t.Fatalf("expected minimal patch, got %d bytes: %q", len(out), out)
	}
}

func TestDiffDimensionsChangedFullRepaint(t *testing.T) {
	prev := NewBuffer(2, 2)
	curr := NewBuffer(4, 4)
	curr.Set(0, 0, Cell{Rune: 'A'})
	out := string(Diff(prev, curr))
	if !strings.HasPrefix(out, "\x1b[2J") {
		t.Fatalf("expected full repaint to start with clear, got %q", out)
	}
}

func TestDiffNilPrevFullRepaint(t *testing.T) {
	curr := NewBuffer(3, 1)
	curr.Set(0, 0, Cell{Rune: 'q'})
	out := string(Diff(nil, curr))
	if !strings.Contains(out, "q") {
		t.Fatalf("missing 'q', got %q", out)
	}
}

func TestDiffSGRApplied(t *testing.T) {
	prev := NewBuffer(2, 1)
	curr := prev.Clone()
	curr.Set(0, 0, Cell{Rune: 'R', FG: Color{Mode: 1, Value: 1}})
	out := string(Diff(prev, curr))
	if !strings.Contains(out, "\x1b[31m") {
		t.Fatalf("expected red SGR, got %q", out)
	}
	// Should reset at end so style doesn't leak.
	if !strings.HasSuffix(out, "\x1b[0m") {
		t.Fatalf("expected trailing reset, got %q", out)
	}
}

func TestDiffCoalescesRunOfCells(t *testing.T) {
	prev := NewBuffer(10, 1)
	curr := prev.Clone()
	for i, r := range []rune{'h', 'e', 'l', 'l', 'o'} {
		curr.Set(i, 0, Cell{Rune: r})
	}
	out := string(Diff(prev, curr))
	// Exactly one CUP, then "hello".
	if strings.Count(out, "\x1b[") != 1 {
		t.Fatalf("expected 1 escape (just CUP), got %q", out)
	}
	if !strings.Contains(out, "hello") {
		t.Fatalf("missing run, got %q", out)
	}
}

func TestRoundTripParseEmitMatches(t *testing.T) {
	// Render a frame, parse it, diff against blank, apply patch back to a
	// fresh buffer, and confirm the result equals the parsed buffer.
	frame := "hello\n\x1b[31mred\x1b[0m world"
	target := Parse(frame, 20, 3)

	blank := NewBuffer(20, 3)
	patch := Diff(blank, target)

	rebuilt := NewBuffer(20, 3)
	ApplyTo(rebuilt, string(patch))

	if !rebuilt.Equal(target) {
		t.Fatalf("round-trip mismatch:\n  target row0 = %q\n  rebuilt row0 = %q",
			rowString(target, 0), rowString(rebuilt, 0))
	}
}

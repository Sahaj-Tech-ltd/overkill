package cellrender

import "testing"

func TestParsePlainText(t *testing.T) {
	b := Parse("hello", 10, 1)
	want := "hello     "
	got := rowString(b, 0)
	if got != want {
		t.Fatalf("row0 = %q, want %q", got, want)
	}
}

func TestParseNewline(t *testing.T) {
	b := Parse("ab\ncd", 4, 2)
	if rowString(b, 0) != "ab  " {
		t.Fatalf("row0 = %q", rowString(b, 0))
	}
	if rowString(b, 1) != "cd  " {
		t.Fatalf("row1 = %q", rowString(b, 1))
	}
}

func TestParseCursorPosition(t *testing.T) {
	// Move to row 2 col 3 (1-based) and write 'X'.
	b := Parse("\x1b[2;3HX", 5, 3)
	if b.Get(2, 1).Rune != 'X' {
		t.Fatalf("expected X at (2,1), got %q", string(b.Get(2, 1).Rune))
	}
}

func TestParseSGRForeground(t *testing.T) {
	b := Parse("\x1b[31mR\x1b[0mN", 4, 1)
	r := b.Get(0, 0)
	if r.FG.Mode != 1 || r.FG.Value != 1 {
		t.Fatalf("R should be ANSI red(1,1), got %+v", r.FG)
	}
	n := b.Get(1, 0)
	if !n.FG.Equal(DefaultColor) {
		t.Fatalf("N should reset to default, got %+v", n.FG)
	}
}

func TestParseSGRTrueColor(t *testing.T) {
	b := Parse("\x1b[38;2;10;20;30mX", 2, 1)
	c := b.Get(0, 0)
	if c.FG.Mode != 3 || c.FG.Value != (10<<16|20<<8|30) {
		t.Fatalf("truecolor: got %+v", c.FG)
	}
}

func TestParseEraseInLine(t *testing.T) {
	// Write "abcde", move to col 2, erase to end. Expect "ab   ".
	b := Parse("abcde\x1b[1;3H\x1b[0K", 5, 1)
	if rowString(b, 0) != "ab   " {
		t.Fatalf("got %q", rowString(b, 0))
	}
}

func TestParseClearScreen(t *testing.T) {
	b := Parse("hello\x1b[2J", 5, 1)
	if rowString(b, 0) != "     " {
		t.Fatalf("expected blank, got %q", rowString(b, 0))
	}
}

func TestParseUnknownSequenceSkipped(t *testing.T) {
	// Private mode set should not corrupt content.
	b := Parse("\x1b[?25hOK", 4, 1)
	if rowString(b, 0) != "OK  " {
		t.Fatalf("got %q", rowString(b, 0))
	}
}

func TestParseTab(t *testing.T) {
	b := Parse("a\tb", 16, 1)
	if b.Get(0, 0).Rune != 'a' || b.Get(8, 0).Rune != 'b' {
		t.Fatalf("tab alignment off: row=%q", rowString(b, 0))
	}
}

func rowString(b *Buffer, y int) string {
	out := make([]rune, b.W)
	for x := 0; x < b.W; x++ {
		r := b.Get(x, y).Rune
		if r == 0 {
			r = ' '
		}
		out[x] = r
	}
	return string(out)
}

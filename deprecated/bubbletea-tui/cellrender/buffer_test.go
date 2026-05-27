package cellrender

import "testing"

func TestNewBufferIsBlank(t *testing.T) {
	b := NewBuffer(4, 2)
	if b.W != 4 || b.H != 2 {
		t.Fatalf("dims: got %dx%d, want 4x2", b.W, b.H)
	}
	for y := 0; y < b.H; y++ {
		for x := 0; x < b.W; x++ {
			if !b.Get(x, y).Equal(blankCell) {
				t.Fatalf("cell(%d,%d) not blank: %+v", x, y, b.Get(x, y))
			}
		}
	}
}

func TestSetGet(t *testing.T) {
	b := NewBuffer(3, 3)
	c := Cell{Rune: 'X', FG: Color{Mode: 3, Value: 0xff0000}}
	b.Set(1, 1, c)
	if !b.Get(1, 1).Equal(c) {
		t.Fatalf("get returned %+v, want %+v", b.Get(1, 1), c)
	}
	// Out-of-bounds is silent.
	b.Set(99, 99, c)
	if !b.Get(99, 99).Equal(blankCell) {
		t.Fatalf("oob get should be blank")
	}
}

func TestEqual(t *testing.T) {
	a := NewBuffer(2, 2)
	b := NewBuffer(2, 2)
	if !a.Equal(b) {
		t.Fatal("two blank buffers should be equal")
	}
	b.Set(0, 0, Cell{Rune: 'a'})
	if a.Equal(b) {
		t.Fatal("buffers differ after Set")
	}
}

func TestResizePreservesContent(t *testing.T) {
	b := NewBuffer(3, 3)
	b.Set(0, 0, Cell{Rune: 'A'})
	b.Set(2, 2, Cell{Rune: 'B'})
	b.Resize(5, 5)
	if b.Get(0, 0).Rune != 'A' {
		t.Fatal("top-left lost on resize")
	}
	if b.Get(2, 2).Rune != 'B' {
		t.Fatal("(2,2) lost on resize")
	}
	if b.Get(4, 4).Rune != ' ' {
		t.Fatal("new region not blank")
	}
}

func TestResizeShrinkClips(t *testing.T) {
	b := NewBuffer(5, 5)
	b.Set(4, 4, Cell{Rune: 'Z'})
	b.Resize(2, 2)
	if b.W != 2 || b.H != 2 {
		t.Fatalf("resize dims wrong")
	}
}

func TestColorEqual(t *testing.T) {
	if !DefaultColor.Equal(Color{}) {
		t.Fatal("DefaultColor != zero Color")
	}
	if (Color{Mode: 3, Value: 0x010203}).Equal(Color{Mode: 3, Value: 0x010204}) {
		t.Fatal("different RGB compared equal")
	}
}

func TestClone(t *testing.T) {
	a := NewBuffer(2, 2)
	a.Set(1, 1, Cell{Rune: 'q'})
	b := a.Clone()
	if !a.Equal(b) {
		t.Fatal("clone not equal")
	}
	b.Set(0, 0, Cell{Rune: 'x'})
	if a.Equal(b) {
		t.Fatal("clone is aliased")
	}
}

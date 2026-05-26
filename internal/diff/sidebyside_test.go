package diff

import "testing"

const sample = `--- a/file.txt
+++ b/file.txt
@@ -1,5 +1,5 @@
 line one
-line two
+line two changed
 line three
-line four
+line four changed
 line five
`

func TestParseHunks_Basic(t *testing.T) {
	hs := ParseHunks(sample)
	if len(hs) != 1 {
		t.Fatalf("want 1 hunk, got %d", len(hs))
	}
	if hs[0].LeftStart != 1 || hs[0].RightStart != 1 {
		t.Errorf("hunk start = (%d,%d) want (1,1)", hs[0].LeftStart, hs[0].RightStart)
	}
	if len(hs[0].Lines) != 7 {
		t.Errorf("want 7 lines, got %d", len(hs[0].Lines))
	}
}

func TestPair_OneForOneSwap(t *testing.T) {
	hs := ParseHunks(sample)
	rows := Pair(hs[0])
	if len(rows) != 5 {
		t.Fatalf("want 5 rows, got %d", len(rows))
	}
	// Row 1 should be context aligned on both sides.
	if rows[0].Left == nil || rows[0].Right == nil || *rows[0].Left != "line one" {
		t.Errorf("row 0 should be aligned context; got %+v", rows[0])
	}
	// Row 1 should be the swap pair.
	if rows[1].Left == nil || rows[1].Right == nil {
		t.Errorf("row 1 should pair delete with add: %+v", rows[1])
	}
	if !rows[1].LeftDel || !rows[1].RightAdd {
		t.Errorf("row 1 should mark both delete and add flags: %+v", rows[1])
	}
}

func TestPair_PureAdd(t *testing.T) {
	in := `@@ -1,1 +1,3 @@
 keep
+new line a
+new line b
`
	rows := Pair(ParseHunks(in)[0])
	if len(rows) != 3 {
		t.Fatalf("want 3 rows, got %d", len(rows))
	}
	if rows[1].Left != nil || rows[1].Right == nil {
		t.Errorf("row 1 should be add-only: %+v", rows[1])
	}
	if rows[2].Left != nil || rows[2].Right == nil {
		t.Errorf("row 2 should be add-only: %+v", rows[2])
	}
}

func TestPair_PureDelete(t *testing.T) {
	in := `@@ -1,3 +1,1 @@
 keep
-gone a
-gone b
`
	rows := Pair(ParseHunks(in)[0])
	if len(rows) != 3 {
		t.Fatalf("want 3 rows, got %d", len(rows))
	}
	if rows[1].Right != nil || rows[1].Left == nil {
		t.Errorf("row 1 should be delete-only: %+v", rows[1])
	}
}

func TestPair_MixedLineNumbers(t *testing.T) {
	hs := ParseHunks(sample)
	rows := Pair(hs[0])
	// Final row is context at left=5 right=5
	last := rows[len(rows)-1]
	if last.LeftNum != 5 || last.RightNum != 5 {
		t.Errorf("last row line numbers = (%d,%d) want (5,5)", last.LeftNum, last.RightNum)
	}
}

func TestParseHunkHeader_BadInput(t *testing.T) {
	l, r := parseHunkHeader("@@ garbage @@")
	if l != 1 || r != 1 {
		t.Errorf("expected fallback to (1,1), got (%d,%d)", l, r)
	}
}

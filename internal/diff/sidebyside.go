// Package diff parses unified diffs and pairs additions with deletions for
// side-by-side rendering. Lives in internal/ so the TUI can render and a
// future ACP exporter can re-use the same structure without import cycles.
package diff

import (
	"strconv"
	"strings"
)

// LineType is the role of a line within a hunk.
type LineType int

const (
	Context LineType = iota
	Add
	Delete
)

// HunkLine is a single line within a unified diff hunk.
type HunkLine struct {
	Type    LineType
	Content string
}

// Hunk groups contiguous lines from a single `@@ -a,b +c,d @@` header.
type Hunk struct {
	LeftStart  int
	RightStart int
	Lines      []HunkLine
}

// LineRow pairs a left (deletion or context) line with a right (addition or
// context) line. Either side may be nil when only one column has content.
type LineRow struct {
	Left     *string
	Right    *string
	LeftNum  int // 0 when Left is nil
	RightNum int // 0 when Right is nil
	LeftDel  bool
	RightAdd bool
}

// ParseHunks reads a unified diff string into a slice of Hunks. Header lines
// (`---`, `+++`, file paths) are skipped.
func ParseHunks(unified string) []Hunk {
	var hunks []Hunk
	var cur *Hunk
	// Drop trailing empty line so a terminating newline doesn't create a
	// phantom blank context row at the end of the last hunk.
	src := strings.TrimRight(unified, "\n")
	for _, line := range strings.Split(src, "\n") {
		if strings.HasPrefix(line, "@@") {
			if cur != nil {
				hunks = append(hunks, *cur)
			}
			ls, rs := parseHunkHeader(line)
			cur = &Hunk{LeftStart: ls, RightStart: rs}
			continue
		}
		if cur == nil {
			continue
		}
		if strings.HasPrefix(line, "---") || strings.HasPrefix(line, "+++") {
			continue
		}
		switch {
		case strings.HasPrefix(line, "+"):
			cur.Lines = append(cur.Lines, HunkLine{Type: Add, Content: line[1:]})
		case strings.HasPrefix(line, "-"):
			cur.Lines = append(cur.Lines, HunkLine{Type: Delete, Content: line[1:]})
		case strings.HasPrefix(line, " "):
			cur.Lines = append(cur.Lines, HunkLine{Type: Context, Content: line[1:]})
		case line == "":
			cur.Lines = append(cur.Lines, HunkLine{Type: Context, Content: ""})
		}
	}
	if cur != nil {
		hunks = append(hunks, *cur)
	}
	return hunks
}

// parseHunkHeader extracts the left and right starting line numbers from a
// `@@ -L,n +R,m @@` header. Returns (1, 1) on parse failure so callers don't
// need to special-case malformed headers.
func parseHunkHeader(line string) (int, int) {
	left, right := 1, 1
	parts := strings.Fields(line)
	for _, p := range parts {
		if strings.HasPrefix(p, "-") {
			s := strings.SplitN(p[1:], ",", 2)[0]
			if v, err := strconv.Atoi(s); err == nil {
				left = v
			}
		}
		if strings.HasPrefix(p, "+") {
			s := strings.SplitN(p[1:], ",", 2)[0]
			if v, err := strconv.Atoi(s); err == nil {
				right = v
			}
		}
	}
	return left, right
}

// Pair walks a Hunk and produces aligned rows for side-by-side display.
//
// Greedy pairing: a contiguous run of Delete lines is matched 1-for-1 with
// the immediately-following run of Add lines. Excess deletes get nil rights;
// excess adds get nil lefts. Context lines appear on both sides at the same
// row.
func Pair(hunk Hunk) []LineRow {
	var rows []LineRow
	leftNum := hunk.LeftStart
	rightNum := hunk.RightStart

	i := 0
	for i < len(hunk.Lines) {
		ln := hunk.Lines[i]
		switch ln.Type {
		case Context:
			c := ln.Content
			rows = append(rows, LineRow{
				Left: &c, Right: &c,
				LeftNum: leftNum, RightNum: rightNum,
			})
			leftNum++
			rightNum++
			i++
		case Delete, Add:
			// Collect contiguous deletes then immediately-following adds.
			dels, adds := []string{}, []string{}
			for i < len(hunk.Lines) && hunk.Lines[i].Type == Delete {
				dels = append(dels, hunk.Lines[i].Content)
				i++
			}
			for i < len(hunk.Lines) && hunk.Lines[i].Type == Add {
				adds = append(adds, hunk.Lines[i].Content)
				i++
			}
			n := max(len(dels), len(adds))
			for k := 0; k < n; k++ {
				row := LineRow{}
				if k < len(dels) {
					s := dels[k]
					row.Left = &s
					row.LeftNum = leftNum
					row.LeftDel = true
					leftNum++
				}
				if k < len(adds) {
					s := adds[k]
					row.Right = &s
					row.RightNum = rightNum
					row.RightAdd = true
					rightNum++
				}
				rows = append(rows, row)
			}
		}
	}
	return rows
}

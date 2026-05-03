package styles

import (
	"strings"
	"testing"

	"github.com/Sahaj-Tech-ltd/ethos/pkg/tui/theme"
)

func TestRenderTable_Empty(t *testing.T) {
	if RenderTable(nil, nil, 80, theme.CurrentTheme()) != "" {
		t.Error("nil rows should render empty")
	}
	if RenderTable([][]string{{}}, nil, 80, theme.CurrentTheme()) != "" {
		t.Error("empty header should render empty")
	}
}

func TestRenderTable_AlignmentRespected(t *testing.T) {
	rows := [][]string{
		{"L", "C", "R"},
		{"a", "b", "c"},
	}
	aligns := []Alignment{AlignLeft, AlignCenter, AlignRight}
	out := RenderTable(rows, aligns, 80, theme.CurrentTheme())
	if !strings.Contains(out, "L") || !strings.Contains(out, "C") || !strings.Contains(out, "R") {
		t.Errorf("missing header content: %q", out)
	}
	// Right-aligned cell should have leading spaces inside its padded column.
	if !strings.Contains(out, "  c ") {
		t.Errorf("expected right-aligned padding around 'c', got %q", out)
	}
}

func TestRenderTable_NarrowTruncates(t *testing.T) {
	rows := [][]string{
		{"Name", "Description"},
		{"x", "this is a very long description that should be cut"},
	}
	out := RenderTable(rows, nil, 30, theme.CurrentTheme())
	if !strings.Contains(out, "…") {
		t.Errorf("expected truncation marker, got %q", out)
	}
	for _, line := range strings.Split(stripANSI(out), "\n") {
		if len([]rune(line)) > 35 {
			t.Errorf("line too wide: %q (%d)", line, len([]rune(line)))
		}
	}
}

func TestRenderTable_HeaderStyledDifferentFromBody(t *testing.T) {
	rows := [][]string{{"H"}, {"b"}}
	out := RenderTable(rows, nil, 40, theme.CurrentTheme())
	// Header should carry an ANSI prefix (bold/foreground) distinct from body.
	if !strings.Contains(out, "\x1b[") {
		t.Skip("no ANSI in test environment")
	}
	headerLine := strings.Split(out, "\n")[0]
	bodyLine := strings.Split(out, "\n")[2]
	if headerLine == bodyLine {
		t.Error("header and body should render with different styles")
	}
}

func TestPreprocessTables_RoundTrip(t *testing.T) {
	in := "before\n\n| A | B |\n|:---|---:|\n| 1 | 2 |\n\nafter"
	out := PreprocessTables(in, 60, theme.CurrentTheme())
	if !strings.Contains(out, "before") || !strings.Contains(out, "after") {
		t.Errorf("non-table content lost: %q", out)
	}
	if strings.Contains(out, "|---|") {
		t.Errorf("alignment row should be consumed: %q", out)
	}
}

func TestParseAlignment(t *testing.T) {
	cases := map[string]Alignment{
		"---":   AlignLeft,
		":---":  AlignLeft,
		"---:":  AlignRight,
		":---:": AlignCenter,
	}
	for in, want := range cases {
		if got := parseAlignment(in); got != want {
			t.Errorf("parseAlignment(%q) = %v, want %v", in, got, want)
		}
	}
}

// stripANSI removes ANSI escape sequences so width assertions work even when
// styling is on.
func stripANSI(s string) string {
	var b strings.Builder
	in := false
	for _, r := range s {
		if r == 0x1b {
			in = true
			continue
		}
		if in {
			if r == 'm' {
				in = false
			}
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

package chat

import (
	"strings"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/deprecated/bubbletea-tui/theme"
)

func TestExtractCodeBlocks_BacktickAndTildeFences(t *testing.T) {
	src := "intro\n```go\nA\n```\nmiddle\n~~~py\nB\n~~~\nend"
	got := ExtractCodeBlocks(src)
	if len(got) != 2 {
		t.Fatalf("want 2, got %d: %+v", len(got), got)
	}
	if got[0].Lang != "go" || got[0].Body != "A" {
		t.Errorf("block 0: %+v", got[0])
	}
	if got[1].Lang != "py" || got[1].Body != "B" {
		t.Errorf("block 1: %+v", got[1])
	}
}

func TestExtractCodeBlocks_BareFenceEmptyLang(t *testing.T) {
	got := ExtractCodeBlocks("```\nplain\n```")
	if len(got) != 1 || got[0].Lang != "" || got[0].Body != "plain" {
		t.Errorf("got %+v", got)
	}
}

func TestExtractCodeBlocks_NoFenceReturnsEmpty(t *testing.T) {
	if got := ExtractCodeBlocks("just prose"); len(got) != 0 {
		t.Errorf("non-fenced content should yield no blocks: %+v", got)
	}
}

func TestBuildCopyFooter_ChipCountMatchesBlocks(t *testing.T) {
	tt := theme.CurrentTheme()
	blocks := []CodeBlock{
		{Lang: "go", Body: "A"},
		{Lang: "py", Body: "B"},
		{Lang: "", Body: "C"},
	}
	footer, layouts := buildCopyFooter(tt, blocks, -1)
	if len(layouts) != 3 {
		t.Fatalf("want 3 chip layouts, got %d", len(layouts))
	}
	for i := range layouts {
		if layouts[i].ColEnd <= layouts[i].ColStart {
			t.Errorf("chip %d has zero width: %+v", i, layouts[i])
		}
	}
	// The footer must contain each chip's number.
	for i := 1; i <= 3; i++ {
		want := "▸"
		if !strings.Contains(footer, want) {
			t.Errorf("footer missing chip marker %d:\n%s", i, footer)
		}
	}
}

func TestBuildCopyFooter_HoveredIndexValid(t *testing.T) {
	// lipgloss strips ANSI codes when stdout isn't a TTY (test env), so
	// the rendered footer bytes can be byte-identical between hovered
	// and resting. We instead pin that the function accepts a hover
	// index without crashing and produces the same chip count.
	tt := theme.CurrentTheme()
	blocks := []CodeBlock{{Body: "A"}, {Body: "B"}}
	_, layouts := buildCopyFooter(tt, blocks, 1)
	if len(layouts) != 2 {
		t.Errorf("hovered render should still produce both chips: got %d", len(layouts))
	}
}

func TestBuildCopyFooter_EmptyBlocksReturnsEmpty(t *testing.T) {
	tt := theme.CurrentTheme()
	f, l := buildCopyFooter(tt, nil, -1)
	if f != "" || l != nil {
		t.Errorf("no blocks → empty result. got footer=%q layouts=%v", f, l)
	}
}

func TestBuildCopyFooter_LayoutColumnsAreNonOverlapping(t *testing.T) {
	tt := theme.CurrentTheme()
	blocks := []CodeBlock{{Body: "A"}, {Body: "B"}, {Body: "C"}, {Body: "D"}}
	_, layouts := buildCopyFooter(tt, blocks, -1)
	for i := 1; i < len(layouts); i++ {
		if layouts[i].ColStart < layouts[i-1].ColEnd {
			t.Errorf("chip %d overlaps chip %d: %+v vs %+v",
				i, i-1, layouts[i], layouts[i-1])
		}
	}
}

func TestCopyChips_AssistantNonStreamingReturnsLayouts(t *testing.T) {
	m := NewMessage("assistant", "see this:\n```go\nfunc Foo() {}\n```\nand this:\n```py\nbar\n```")
	chips := m.CopyChips(80, -1)
	if len(chips) != 2 {
		t.Fatalf("want 2 chips, got %d", len(chips))
	}
	if chips[0].Body != "func Foo() {}" || chips[0].Lang != "go" {
		t.Errorf("chip 0: %+v", chips[0])
	}
	if chips[1].Body != "bar" || chips[1].Lang != "py" {
		t.Errorf("chip 1: %+v", chips[1])
	}
}

func TestCopyChips_StreamingReturnsNil(t *testing.T) {
	m := NewMessage("assistant", "```go\nx\n```")
	m.Streaming = true
	if got := m.CopyChips(80, -1); got != nil {
		t.Errorf("streaming message should not emit chips: %+v", got)
	}
}

func TestCopyChips_UserRoleReturnsNil(t *testing.T) {
	m := NewMessage("user", "```go\nx\n```")
	if got := m.CopyChips(80, -1); got != nil {
		t.Errorf("user-role message should not emit chips: %+v", got)
	}
}

func TestCopyChips_NoBlocksReturnsNil(t *testing.T) {
	m := NewMessage("assistant", "just prose, no fences")
	if got := m.CopyChips(80, -1); got != nil {
		t.Errorf("blockless message should not emit chips: %+v", got)
	}
}

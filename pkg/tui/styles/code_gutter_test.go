package styles

import (
	"strings"
	"testing"
)

func TestAddCodeBlockGutters_LongBlockGetsNumbers(t *testing.T) {
	src := "before\n```go\n" +
		"line 1\nline 2\nline 3\nline 4\nline 5\nline 6\n" +
		"```\nafter\n"
	out := addCodeBlockGutters(src)
	if !strings.Contains(out, "1│ line 1") {
		t.Errorf("expected gutter on first line, got:\n%s", out)
	}
	if !strings.Contains(out, "6│ line 6") {
		t.Errorf("expected gutter on last line, got:\n%s", out)
	}
	if !strings.Contains(out, "```go") {
		t.Errorf("fence opener should be preserved, got:\n%s", out)
	}
}

func TestAddCodeBlockGutters_ShortBlockSkipped(t *testing.T) {
	src := "```\necho hi\n```\n"
	out := addCodeBlockGutters(src)
	if strings.Contains(out, "│") {
		t.Errorf("short block should not get gutters, got:\n%s", out)
	}
}

func TestAddCodeBlockGutters_TildeFenceWorks(t *testing.T) {
	src := "~~~python\n" +
		"a\nb\nc\nd\ne\nf\n" +
		"~~~\n"
	out := addCodeBlockGutters(src)
	if !strings.Contains(out, "1│ a") {
		t.Errorf("tilde fence should be detected, got:\n%s", out)
	}
}

func TestAddCodeBlockGutters_NoFenceUnchanged(t *testing.T) {
	src := "just prose\nmore prose\n"
	out := addCodeBlockGutters(src)
	if out != src {
		t.Errorf("non-code content should pass through unchanged.\nwant: %q\ngot:  %q", src, out)
	}
}

func TestAddCodeBlockGutters_WidthScalesWithDigits(t *testing.T) {
	var b strings.Builder
	b.WriteString("```\n")
	for i := 0; i < 12; i++ {
		b.WriteString("line\n")
	}
	b.WriteString("```\n")
	out := addCodeBlockGutters(b.String())
	// 12 lines → 2-digit width → " 1│" (right-aligned with a leading space).
	if !strings.Contains(out, " 1│ line") {
		t.Errorf("expected right-aligned 2-digit gutter for 12 lines, got:\n%s", out)
	}
	if !strings.Contains(out, "12│ line") {
		t.Errorf("expected '12│' for line 12, got:\n%s", out)
	}
}

func TestAddCodeBlockGutters_UnterminatedFenceFlushes(t *testing.T) {
	src := "```\n" +
		"a\nb\nc\nd\ne\nf\n"
	out := addCodeBlockGutters(src)
	if !strings.Contains(out, "1│ a") {
		t.Errorf("unterminated fence should still get flushed with gutters, got:\n%s", out)
	}
}

func TestAddCodeBlockGutters_PreservesNoTrailingNewline(t *testing.T) {
	src := "no trailing newline"
	out := addCodeBlockGutters(src)
	if strings.HasSuffix(out, "\n") {
		t.Errorf("should not add a trailing newline when source had none, got: %q", out)
	}
}

package tui

import (
	"strings"
	"testing"
)

func TestPreviewFirstLine_BelowLimit(t *testing.T) {
	if got := previewFirstLine("hello world"); got != "hello world" {
		t.Errorf("got %q", got)
	}
}

func TestPreviewFirstLine_TruncatesPastLimit(t *testing.T) {
	long := strings.Repeat("x", 60)
	got := previewFirstLine(long)
	if !strings.HasSuffix(got, "...") {
		t.Errorf("expected ellipsis, got %q", got)
	}
	if len(got) != 40 {
		t.Errorf("expected 40-char preview, got %d: %q", len(got), got)
	}
}

func TestPreviewFirstLine_StopsAtNewline(t *testing.T) {
	if got := previewFirstLine("first\nsecond"); got != "first" {
		t.Errorf("multi-line should stop at newline: %q", got)
	}
}

func TestPreviewFirstLine_EmptyString(t *testing.T) {
	if got := previewFirstLine(""); got != "" {
		t.Errorf("empty in → empty out, got %q", got)
	}
}

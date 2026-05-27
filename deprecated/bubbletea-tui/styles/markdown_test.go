package styles

import (
	"strings"
	"testing"
)

func TestMarkdown_Render(t *testing.T) {
	result := RenderMarkdown("**bold** and *italic*", 80)
	if result == "" {
		t.Error("should render something")
	}
	if result == "**bold** and *italic*" {
		t.Error("should transform markdown")
	}
}

func TestMarkdown_CodeBlock(t *testing.T) {
	input := "```go\nfmt.Println(\"hello\")\n```"
	result := RenderMarkdown(input, 80)
	if result == "" {
		t.Error("should render")
	}
	if result == input {
		t.Error("should transform code block")
	}
}

func TestMarkdown_Table(t *testing.T) {
	input := "| A | B |\n|---|---|\n| 1 | 2 |"
	result := RenderMarkdown(input, 80)
	if result == "" {
		t.Error("should render")
	}
}

func TestMarkdown_Truncate(t *testing.T) {
	longText := strings.Repeat("word ", 100)
	result := RenderMarkdown(longText, 40)
	if result == "" {
		t.Error("should render")
	}
}

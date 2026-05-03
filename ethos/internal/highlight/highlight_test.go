package highlight

import (
	"strings"
	"testing"
)

func TestHighlightGoEmitsANSI(t *testing.T) {
	src := "package main\n\nfunc main() { println(\"hi\") }\n"
	out := Highlight(src, "go", DefaultTheme)
	if !strings.Contains(out, "\x1b[") {
		t.Fatalf("expected ANSI escape sequences in highlighted output, got %q", out)
	}
	if !strings.Contains(out, "main") {
		t.Fatalf("expected source content preserved, got %q", out)
	}
}

func TestHighlightUnknownLangPassthrough(t *testing.T) {
	src := "totally not code"
	out := Highlight(src, "esoterica-9", DefaultTheme)
	// chroma may auto-detect; what we care about is no panic and content kept.
	if !strings.Contains(out, "totally not code") {
		t.Fatalf("expected content kept, got %q", out)
	}
}

func TestLangFromFence(t *testing.T) {
	cases := map[string]string{
		"```go":            "go",
		"go":               "go",
		"```ts {linenos}":  "ts",
		"```python\nfoo":   "python",
		"":                 "",
	}
	for in, want := range cases {
		if got := LangFromFence(in); got != want {
			t.Errorf("LangFromFence(%q)=%q want %q", in, got, want)
		}
	}
}

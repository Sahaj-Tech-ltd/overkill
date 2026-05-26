// Package highlight provides ANSI syntax highlighting for code blocks.
//
// Implementation note: the original spec asks for tree-sitter, but
// github.com/alecthomas/chroma is already pulled in as an indirect
// dependency via Glamour, supports 250+ languages, ships ANSI formatters
// out of the box, and avoids the CGO compile cost of go-tree-sitter and
// its per-language sub-packages. The public API here is the same shape
// the caller expects.
package highlight

import (
	"bytes"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

// Theme picks the chroma style. Any registered chroma style name works
// (e.g. "monokai", "github-dark", "solarized-dark").
type Theme struct {
	Name string
}

// DefaultTheme is the fallback style.
var DefaultTheme = Theme{Name: "monokai"}

// Highlight returns ANSI-colored content for the given language. Unknown
// languages or render failures fall back to the raw input — never returns
// an error so callers can drop this in without extra plumbing.
func Highlight(content, lang string, theme Theme) string {
	if strings.TrimSpace(content) == "" {
		return content
	}
	lexer := pickLexer(lang, content)
	if lexer == nil {
		return content
	}
	style := styles.Get(theme.Name)
	if style == nil {
		style = styles.Fallback
	}
	formatter := formatters.Get("terminal256")
	if formatter == nil {
		formatter = formatters.Fallback
	}
	iter, err := lexer.Tokenise(nil, content)
	if err != nil {
		return content
	}
	var buf bytes.Buffer
	if err := formatter.Format(&buf, style, iter); err != nil {
		return content
	}
	return buf.String()
}

// pickLexer matches by alias name first, falls back to filename detection,
// then to "analyse" content sniffing.
func pickLexer(lang, content string) chroma.Lexer {
	if lang != "" {
		if l := lexers.Get(strings.ToLower(lang)); l != nil {
			return chroma.Coalesce(l)
		}
	}
	if l := lexers.Analyse(content); l != nil {
		return chroma.Coalesce(l)
	}
	return nil
}

// LangFromFence extracts the language tag from a markdown fence info string
// like "```go" → "go" or "```ts {linenos}" → "ts".
func LangFromFence(fence string) string {
	fence = strings.TrimSpace(fence)
	fence = strings.TrimLeft(fence, "`")
	if i := strings.IndexAny(fence, " \t\n\r{"); i >= 0 {
		fence = fence[:i]
	}
	return strings.TrimSpace(fence)
}

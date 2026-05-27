package styles

import (
	"fmt"
	"strings"
)

// gutterMinLines is the threshold below which we don't add line numbers.
// Single-line shell snippets and 2-3 line examples read cleaner without
// the gutter; long blocks (function bodies, log output) benefit.
const gutterMinLines = 6

// addCodeBlockGutters walks fenced code blocks in markdown and prefixes
// each line with a "N│ " gutter. Short blocks are passed through
// untouched so common shell one-liners stay scan-friendly.
//
// We operate on the markdown source (pre-render) because:
//   - Detecting fence boundaries in raw markdown is line-based and cheap;
//     parsing styled ANSI output for the same boundaries is brittle.
//   - The gutter survives glamour's code-block styling because it's part
//     of the code content and glamour doesn't reflow inside fences.
//
// This must NOT run in conceal mode — a user copying out the block would
// get the gutters embedded. RenderMarkdown gates accordingly.
func addCodeBlockGutters(content string) string {
	// Split-but-remember-trailing-newline so we can reproduce the
	// original terminal whitespace shape exactly. strings.Split("a\n", "\n")
	// returns ["a", ""], and rejoining with \n suffixes adds a phantom
	// empty line. Stripping a trailing "" and tracking it separately keeps
	// the output bit-identical for the pass-through path.
	hadTrailingNewline := strings.HasSuffix(content, "\n")
	body := content
	if hadTrailingNewline {
		body = content[:len(content)-1]
	}
	lines := strings.Split(body, "\n")
	var (
		out      strings.Builder
		inFence  bool
		fenceTag string // "```" or "~~~" so we close on the same kind
		buffer   []string
	)

	flush := func() {
		if len(buffer) >= gutterMinLines {
			width := numberWidth(len(buffer))
			for i, line := range buffer {
				fmt.Fprintf(&out, "%*d│ %s\n", width, i+1, line)
			}
		} else {
			for _, line := range buffer {
				out.WriteString(line)
				out.WriteByte('\n')
			}
		}
		buffer = buffer[:0]
	}

	writeLine := func(s string, last bool) {
		out.WriteString(s)
		if !last {
			out.WriteByte('\n')
		}
	}

	for i, line := range lines {
		last := i == len(lines)-1
		trimmed := strings.TrimLeft(line, " \t")
		switch {
		case !inFence && (strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~")):
			inFence = true
			if strings.HasPrefix(trimmed, "```") {
				fenceTag = "```"
			} else {
				fenceTag = "~~~"
			}
			writeLine(line, last)
		case inFence && strings.HasPrefix(trimmed, fenceTag):
			flush()
			inFence = false
			writeLine(line, last)
		case inFence:
			buffer = append(buffer, line)
		default:
			writeLine(line, last)
		}
	}
	// Unterminated fence — flush what we've got rather than swallowing.
	if inFence {
		flush()
	}

	result := out.String()
	if hadTrailingNewline && !strings.HasSuffix(result, "\n") {
		result += "\n"
	}
	return result
}

func numberWidth(n int) int {
	w := 1
	for n >= 10 {
		n /= 10
		w++
	}
	return w
}

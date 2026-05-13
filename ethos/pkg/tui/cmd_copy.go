// Package tui — /copy slash command for code-block extraction.
//
// "Hover-copy" in a terminal is a UX trap: the obvious implementation
// (enable mouse mode + drag-select) conflicts with native terminal
// selection, which is muscle memory for most users. Instead we surface
// a keyboard-driven equivalent — /copy pulls the most recent code
// block from the assistant's reply and pushes it to the system
// clipboard via OSC52, no ANSI styling included.
//
// Behavior:
//
//   /copy           → copy the most recent code block
//   /copy 2         → copy the 2nd-most-recent (1-indexed from newest)
//   /copy all       → concatenate every code block in the session with
//                     blank-line separators
//
// OSC52 is best-effort — terminals without support silently drop the
// sequence. We still print the toast because the block has been
// extracted; the user can see what we tried to copy.
package tui

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

// fencedCodeWithLang captures (lang, body) for both backtick and tilde
// fences. Lang character set covers the common forms (alphanumerics,
// underscores, dashes); exotic tags like "c++" fall through to a bare-
// fence match with empty lang, which the toast labels as "code".
var fencedCodeWithLang = regexp.MustCompile("(?s)(?:```|~~~)([a-zA-Z0-9_-]*)\\s*\\n(.*?)\\n(?:```|~~~)")

// codeBlock is one extracted snippet plus its source language tag for
// the confirmation toast. Lang is best-effort — many fences are bare.
type codeBlock struct {
	Lang string
	Body string
}

// extractCodeBlocks walks every assistant message in history and pulls
// out fenced code blocks. The result is ordered newest-first so the
// caller can index from 1 to "most recent" without re-reversing.
func extractCodeBlocks(history []providers.Message) []codeBlock {
	var out []codeBlock
	// Walk newest-first so the result slice is also newest-first.
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role != "assistant" {
			continue
		}
		blocks := codeBlocksInOrder(history[i].Content)
		// Within a single message, blocks appear in source order. Push
		// them in REVERSE so the overall slice stays newest-first.
		for j := len(blocks) - 1; j >= 0; j-- {
			out = append(out, blocks[j])
		}
	}
	return out
}

// codeBlocksInOrder returns the fenced code blocks of s in the order
// they appear. Used by extractCodeBlocks; broken out so tests can pin
// the within-message ordering separately from the cross-message logic.
func codeBlocksInOrder(s string) []codeBlock {
	matches := fencedCodeWithLang.FindAllStringSubmatch(s, -1)
	out := make([]codeBlock, 0, len(matches))
	for _, m := range matches {
		// m[0]=whole match, m[1]=lang, m[2]=body
		lang := strings.TrimSpace(m[1])
		out = append(out, codeBlock{Lang: lang, Body: m[2]})
	}
	return out
}

// runCopy is the /copy handler. arg is whatever the user typed after
// the command, trimmed.
func (m *appModel) runCopy(arg string) tea.Cmd {
	if m.app == nil || m.app.Agent == nil {
		return m.toastCmd("copy: no active agent", "warning")
	}
	blocks := extractCodeBlocks(m.app.Agent.History())
	if len(blocks) == 0 {
		return m.toastCmd("copy: no code blocks in this session yet", "info")
	}

	arg = strings.TrimSpace(arg)
	switch {
	case arg == "" || arg == "1":
		return m.copyBlock(blocks[0])
	case arg == "all":
		var b strings.Builder
		// Concatenate oldest-first so the resulting paste reads in source
		// order. extractCodeBlocks returned newest-first, so iterate from
		// the back.
		for i := len(blocks) - 1; i >= 0; i-- {
			if i < len(blocks)-1 {
				b.WriteString("\n\n")
			}
			b.WriteString(blocks[i].Body)
		}
		writeOSC52(b.String())
		return m.toastCmd(fmt.Sprintf("copy: %d code block(s) sent to clipboard", len(blocks)), "success")
	default:
		n, err := strconv.Atoi(arg)
		if err != nil || n < 1 {
			return m.toastCmd("copy: usage /copy [n|all]", "warning")
		}
		if n > len(blocks) {
			return m.toastCmd(fmt.Sprintf("copy: only %d code block(s) in this session", len(blocks)), "info")
		}
		return m.copyBlock(blocks[n-1])
	}
}

// copyBlock pushes one code block to the clipboard via OSC52 and emits
// a toast that includes the language tag (when present) and a short
// preview of the first line. The preview matters because OSC52 fails
// silently on unsupported terminals — without a hint the user has no
// confirmation that the right block was picked.
func (m *appModel) copyBlock(b codeBlock) tea.Cmd {
	writeOSC52(b.Body)

	lang := b.Lang
	if lang == "" {
		lang = "code"
	}
	preview := strings.SplitN(b.Body, "\n", 2)[0]
	if len(preview) > 40 {
		preview = preview[:37] + "..."
	}
	return m.toastCmd(fmt.Sprintf("copy: %s — %s", lang, preview), "success")
}

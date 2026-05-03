// Package slack implements a minimal Slack bot daemon for ethos.
//
// We deliberately stay on stdlib only — Slack's API is plain JSON over HTTP
// and a single Socket-Mode WebSocket. Pulling in slack-go/slack would add
// thousands of lines of API surface we never call.
package slack

import (
	"strings"
)

// MarkdownToMrkdwn converts a small subset of CommonMark into Slack's
// "mrkdwn" dialect. Slack uses single asterisks for bold (*bold*), single
// underscores for italics (_italic_), and triple-backtick fences for code.
// Inline code (`code`), links, headings, and lists pass through largely
// unchanged because Slack already understands them.
//
// The conversion is intentionally simple — we walk the string once and
// transform the few constructs whose syntax actually differs. We do NOT try
// to be a full CommonMark parser; for the agent's output (which is mostly
// plain text + the occasional bold/code) this is sufficient and avoids the
// brittleness of regex-driven markdown rewrites.
func MarkdownToMrkdwn(s string) string {
	if s == "" {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))

	i := 0
	for i < len(s) {
		// Pass through fenced code blocks verbatim.
		if strings.HasPrefix(s[i:], "```") {
			end := strings.Index(s[i+3:], "```")
			if end < 0 {
				b.WriteString(s[i:])
				return b.String()
			}
			b.WriteString(s[i : i+3+end+3])
			i += 3 + end + 3
			continue
		}
		// Pass through inline code verbatim.
		if s[i] == '`' {
			end := strings.IndexByte(s[i+1:], '`')
			if end < 0 {
				b.WriteByte(s[i])
				i++
				continue
			}
			b.WriteString(s[i : i+1+end+1])
			i += 1 + end + 1
			continue
		}
		// Bold: **text** → *text*. Slack's bold is a single asterisk; CommonMark
		// uses doubles. We require both delimiters to appear on the same line so
		// we don't accidentally swallow multi-paragraph regions when the source
		// is malformed.
		if strings.HasPrefix(s[i:], "**") {
			rest := s[i+2:]
			nl := strings.IndexByte(rest, '\n')
			search := rest
			if nl >= 0 {
				search = rest[:nl]
			}
			end := strings.Index(search, "**")
			if end > 0 {
				b.WriteByte('*')
				b.WriteString(rest[:end])
				b.WriteByte('*')
				i += 2 + end + 2
				continue
			}
		}
		// Italic: *text* (CommonMark) → _text_ (Slack). We only convert when the
		// asterisk is clearly used as emphasis (not preceded by a word char and
		// followed by non-space) so we don't mangle bullet lists or math.
		if s[i] == '*' && (i == 0 || isBoundary(s[i-1])) && i+1 < len(s) && s[i+1] != ' ' && s[i+1] != '*' {
			rest := s[i+1:]
			nl := strings.IndexByte(rest, '\n')
			search := rest
			if nl >= 0 {
				search = rest[:nl]
			}
			end := strings.IndexByte(search, '*')
			if end > 0 {
				b.WriteByte('_')
				b.WriteString(rest[:end])
				b.WriteByte('_')
				i += 1 + end + 1
				continue
			}
		}
		// Headings (#, ##, ###) → bold line. Slack has no native headings.
		if (i == 0 || s[i-1] == '\n') && s[i] == '#' {
			j := i
			for j < len(s) && s[j] == '#' {
				j++
			}
			if j < len(s) && s[j] == ' ' {
				lineEnd := strings.IndexByte(s[j+1:], '\n')
				if lineEnd < 0 {
					b.WriteByte('*')
					b.WriteString(s[j+1:])
					b.WriteByte('*')
					return b.String()
				}
				b.WriteByte('*')
				b.WriteString(s[j+1 : j+1+lineEnd])
				b.WriteByte('*')
				b.WriteByte('\n')
				i = j + 1 + lineEnd + 1
				continue
			}
		}
		// Markdown links [text](url) → Slack <url|text>.
		if s[i] == '[' {
			closeBracket := strings.IndexByte(s[i+1:], ']')
			if closeBracket >= 0 && i+1+closeBracket+1 < len(s) && s[i+1+closeBracket+1] == '(' {
				start := i + 1 + closeBracket + 2
				closeParen := strings.IndexByte(s[start:], ')')
				if closeParen >= 0 {
					text := s[i+1 : i+1+closeBracket]
					url := s[start : start+closeParen]
					b.WriteByte('<')
					b.WriteString(url)
					b.WriteByte('|')
					b.WriteString(text)
					b.WriteByte('>')
					i = start + closeParen + 1
					continue
				}
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func isBoundary(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '(' || c == '[' || c == '>'
}

// EscapeMrkdwn escapes the three characters Slack treats as control
// characters in user-supplied text: &, <, >. Use this when injecting
// untrusted content (tool args, error messages) that should NOT be parsed
// as Slack formatting.
func EscapeMrkdwn(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}

// FormatToolCall renders a tool invocation as a collapsed Slack block.
// Long argument payloads are truncated so we don't blow Slack's 40k message
// limit on a single noisy tool.
func FormatToolCall(name, args string) string {
	const maxArgs = 800
	args = strings.TrimSpace(args)
	if len(args) > maxArgs {
		args = args[:maxArgs] + "\n…(truncated)"
	}
	if args == "" {
		return ":wrench: `" + name + "`"
	}
	return ":wrench: `" + name + "`\n```\n" + args + "\n```"
}

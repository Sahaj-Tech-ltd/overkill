package plugin

import (
	"context"
	"strings"
)

const (
	// maxSnippetTitleChars is the maximum length of a snippet title
	// after sanitization. Longer titles are truncated with "…".
	maxSnippetTitleChars = 80

	// maxSnippetContentChars caps the content of a single snippet.
	// Individual plugins that blow past this likely have a bug.
	maxSnippetContentChars = 8000
)

// AssembleSnippets turns a slice of context snippets into a system-prompt
// fragment. Returns "" when there's nothing to add. Used by the agent
// before each model call.
//
// maxTotalChars is a global budget for the entire assembled fragment
// (including headers). Snippets are added until the budget is exhausted;
// leftover snippets are dropped and a truncation warning is appended.
func AssembleSnippets(snippets []ContextSnippet, maxTotalChars int) string {
	if len(snippets) == 0 {
		return ""
	}
	var b strings.Builder
	header := "\n\n# Plugin context\n"
	b.WriteString(header)
	remaining := maxTotalChars - len(header)
	truncated := false
	added := 0

	for _, s := range snippets {
		title := sanitizeSnippetTitle(s.Title)
		content := sanitizeSnippetContent(s.Content)

		// Estimate the bytes this snippet will add.
		entry := "\n## " + title + "\n" + content + "\n"
		if len(entry) > remaining {
			truncated = true
			break
		}
		b.WriteString(entry)
		remaining -= len(entry)
		added++
	}

	if truncated && added > 0 {
		b.WriteString("\n*(some plugin context omitted — budget exhausted)*\n")
	}
	return b.String()
}

// sanitizeSnippetTitle strips newlines, enforces a max length, and escapes
// markdown structure characters to prevent prompt injection via snippet titles.
func sanitizeSnippetTitle(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "(untitled)"
	}
	// Strip all newlines — they can break prompt structure.
	raw = strings.ReplaceAll(raw, "\n", " ")
	raw = strings.ReplaceAll(raw, "\r", " ")
	// Escape markdown heading/structure characters that could confuse the model.
	raw = strings.ReplaceAll(raw, "#", "\\#")
	// Truncate to max length.
	if len(raw) > maxSnippetTitleChars {
		raw = raw[:maxSnippetTitleChars-1] + "…"
	}
	return raw
}

// sanitizeSnippetContent trims trailing newlines and enforces a per-snippet cap.
func sanitizeSnippetContent(raw string) string {
	raw = strings.TrimRight(raw, "\n")
	if len(raw) > maxSnippetContentChars {
		raw = raw[:maxSnippetContentChars-20] + "\n\n*(truncated)*"
	}
	return raw
}

// ProvideAndAssemble is a convenience wrapper around Manager.Provide +
// AssembleSnippets. Safe to call when m is nil.
//
// maxTotalChars is forwarded to AssembleSnippets as the global budget.
func ProvideAndAssemble(ctx context.Context, m *Manager, prompt, sessionID string, maxTotalChars int) string {
	if m == nil {
		return ""
	}
	return AssembleSnippets(m.Provide(ctx, prompt, sessionID), maxTotalChars)
}

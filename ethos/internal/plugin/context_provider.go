package plugin

import (
	"context"
	"strings"
)

// AssembleSnippets turns a slice of context snippets into a system-prompt
// fragment. Returns "" when there's nothing to add. Used by the agent
// before each model call.
func AssembleSnippets(snippets []ContextSnippet) string {
	if len(snippets) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n# Plugin context\n")
	for _, s := range snippets {
		title := strings.TrimSpace(s.Title)
		if title == "" {
			title = "(untitled)"
		}
		b.WriteString("\n## ")
		b.WriteString(title)
		b.WriteString("\n")
		b.WriteString(strings.TrimRight(s.Content, "\n"))
		b.WriteString("\n")
	}
	return b.String()
}

// ProvideAndAssemble is a convenience wrapper around Manager.Provide +
// AssembleSnippets. Safe to call when m is nil.
func ProvideAndAssemble(ctx context.Context, m *Manager, prompt, sessionID string) string {
	if m == nil {
		return ""
	}
	return AssembleSnippets(m.Provide(ctx, prompt, sessionID))
}

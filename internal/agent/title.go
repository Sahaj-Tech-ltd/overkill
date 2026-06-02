package agent

import (
	"context"
	"strings"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

// GenerateTitle asks the provider for a short, descriptive session title
// based on the current conversation history. Used by the TUI immediately
// after the first user-assistant exchange to replace the placeholder
// "session 2026-01-01 12:34" with something useful (master plan §4.6).
//
// Best-effort: a provider error returns ("", err) and the caller falls
// back to the placeholder. Capped at 80 output tokens and a single
// completion call — this should not be a hot-path cost.
func (a *Agent) GenerateTitle(ctx context.Context) (string, error) {
	a.mu.RLock()
	hist := make([]providers.Message, 0, len(a.history))
	for _, m := range a.history {
		if m.Role != "user" && m.Role != "assistant" {
			continue
		}
		hist = append(hist, m)
	}
	model := a.model
	provider := a.provider
	a.mu.RUnlock()

	if provider == nil || len(hist) == 0 {
		return "", nil
	}

	// Trim history to the first two messages — title needs gist, not
	// the entire transcript. Cheap-model context budget tight by design.
	if len(hist) > 2 {
		hist = hist[:2]
	}

	req := providers.Request{
		Model:        model,
		Messages:     hist,
		MaxTokens:    80,
		SystemPrompt: "Output ONLY a 3-7 word title for this conversation. No quotes, no punctuation at the end, no preamble. Just the title.",
	}
	resp, err := provider.Complete(ctx, req)
	if err != nil {
		return "", err
	}
	title := strings.TrimSpace(resp.Content)
	// Defensive cleanup: strip wrapping quotes / trailing period if the
	// model ignored the instruction. Truncate to a hard 80-rune cap so
	// we never persist a runaway title.
	title = strings.Trim(title, `"'`)
	title = strings.TrimSuffix(title, ".")
	title = strings.TrimSpace(title)
	if r := []rune(title); len(r) > 80 {
		title = string(r[:80])
	}
	return title, nil
}

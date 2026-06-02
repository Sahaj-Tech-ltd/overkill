package agent

import "github.com/Sahaj-Tech-ltd/overkill/internal/walls/truthsource"

// ResponseFilter is the tiny interface the agent uses to transform
// the assistant's content before it's committed to history (master
// plan §4.10 sycophancy reducer wire-up).
//
// Filters run AFTER the stream finishes assembling — they can't peek
// at chunks mid-stream, only at the final content. This preserves the
// streaming UX (tokens flow to the TUI as they arrive) while still
// cleaning the canonical record the LLM sees on its next turn.
//
// Filters MUST be safe to call on an empty string, and SHOULD be
// idempotent (filter(filter(s)) == filter(s)) so a request that flows
// through twice isn't double-stripped.
type ResponseFilter interface {
	Filter(content string) string
}

// SetResponseFilter installs the post-stream content cleaner. Pass nil
// to disable.
func (a *Agent) SetResponseFilter(f ResponseFilter) {
	a.mu.Lock()
	a.responseFilter = f
	a.mu.Unlock()
}

// applyResponseFilter is the agent-internal hook. Nil-safe + panic-
// safe: a misbehaving filter falls back to the original content so a
// broken filter never drops the turn. Empty filter result on non-
// empty input also falls back — ship the raw text rather than nothing.
func (a *Agent) applyResponseFilter(content string) (out string) {
	a.mu.RLock()
	f := a.responseFilter
	a.mu.RUnlock()
	if f == nil {
		return content
	}
	// Named return so a panic recovery can override the value.
	out = content
	defer func() {
		if r := recover(); r != nil {
			out = content
		}
	}()
	filtered := f.Filter(content)
	if filtered == "" && content != "" {
		return content
	}
	// Observe-only: scan for "user is not source of truth" redirect patterns.
	// High-severity findings are logged to the behavioral journal via emit so
	// they accumulate in the regression bank. The response text is never
	// modified — this is a detection wall, not a blocking wall.
	a.observeTruthsource(filtered)
	return filtered
}

// observeTruthsource runs the truthsource wall against the response text
// and emits a behavioral_flag event for any high-severity finding. It is
// a best-effort, fire-and-forget observer — it never modifies the response.
func (a *Agent) observeTruthsource(content string) {
	result := truthsource.Check(content)
	if !result.HasIssue || !truthsource.HasHighSeverity(result) {
		return
	}
	excerpts := make([]string, 0, len(result.Findings))
	for _, f := range result.Findings {
		if f.Severity == "high" {
			excerpts = append(excerpts, f.Excerpt)
		}
	}
	a.emit("behavioral_flag", map[string]any{
		"wall":     "truthsource",
		"severity": "high",
		"excerpts": excerpts,
		"rule":     "8.7.6",
	})
}

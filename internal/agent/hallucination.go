// Package agent — hallucination scanner integration (Batch G3).
//
// Sits between the streamed response assembly and the response
// filter. After the model finishes a turn we hand the assembled
// content + the session's evidence corpus to the scanner; if the
// scanner annotates anything, the annotated content goes into
// history instead of the raw text. The model reads the [?] markers
// on its next turn and learns to re-ground.
//
// The interface lives here so internal/agent doesn't import the
// concrete internal/halluscan package — same separation we use for
// the post-write verifier and memory orchestrator.
package agent

import "github.com/Sahaj-Tech-ltd/overkill/internal/providers"

// historyView adapts a providers.Message slice into the local
// content-only view that buildEvidenceCorpus walks. Filters empty
// content so we don't waste budget bytes on empties.
func historyView(history []providers.Message) []providersMessageView {
	out := make([]providersMessageView, 0, len(history))
	for _, m := range history {
		if m.Content == "" {
			continue
		}
		out = append(out, providersMessageView{content: m.Content})
	}
	return out
}

// HallucinationScanner is the minimal surface the agent calls after
// each assistant turn. The wiring layer (cmd/overkill) plugs in the
// concrete scanner from internal/halluscan.
type HallucinationScanner interface {
	// Scan inspects content against the supplied evidence corpus
	// (concatenated session history + tool outputs) and returns
	// content annotated with [?] markers after unverified
	// references. When nothing was flagged, returns content
	// unchanged.
	Scan(content, evidence string) string
}

// SetHallucinationScanner wires the scanner. nil disables. Safe to
// call at any time; affects the NEXT turn's post-stream pass.
func (a *Agent) SetHallucinationScanner(s HallucinationScanner) {
	if a == nil {
		return
	}
	a.mu.Lock()
	a.hallucinationScanner = s
	a.mu.Unlock()
}

// getHallucinationScanner returns the wired scanner under read lock.
// nil is the legal "off" state.
func (a *Agent) getHallucinationScanner() HallucinationScanner {
	if a == nil {
		return nil
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.hallucinationScanner
}

// SessionBookmarkStore is the minimal surface the agent needs to persist
// user bookmarks (§7.4). Wired by cmd/overkill with a gateway.BookmarkStore
// adapter when a PostgreSQL connection is available.
type SessionBookmarkStore interface {
	Save(sessionID, label string) error
}

// SetSessionBookmarkStore wires a bookmark store for session bookmarking.
// nil disables. Safe to call at any time.
func (a *Agent) SetSessionBookmarkStore(s SessionBookmarkStore) {
	if a == nil {
		return
	}
	a.mu.Lock()
	a.bookmarkStore = s
	a.mu.Unlock()
}

// buildEvidenceCorpus concatenates everything the agent considers
// "real" for this session — user messages, prior assistant turns,
// tool outputs, system prompt. The scanner uses this as the
// allowed-identifiers source: any backtick-quoted reference that
// doesn't appear here is a candidate hallucination.
//
// We include the CURRENT message's tool outputs (assembled this
// turn) too — those were just produced and are valid evidence.
//
// Bounded: 256KB. Long sessions have megabytes of tool output;
// scanning that on every turn would cost. We sample newest-first
// because the model's claims are usually about recent state.
func (a *Agent) buildEvidenceCorpus(currentToolOutputs []string) string {
	const maxBytes = 256 * 1024
	a.mu.RLock()
	histCopy := append([]providersMessageView(nil), historyView(a.history)...)
	sys := a.systemPrompt
	a.mu.RUnlock()

	var b corpusBuilder
	b.cap = maxBytes
	// Newest-first walk so the most recent evidence wins the budget.
	for i := len(histCopy) - 1; i >= 0; i-- {
		if !b.add(histCopy[i].content) {
			break
		}
	}
	for _, out := range currentToolOutputs {
		if !b.add(out) {
			break
		}
	}
	b.add(sys)
	return b.String()
}

// providersMessageView is the minimal slice of providers.Message that
// buildEvidenceCorpus needs. Avoiding a direct providers import here
// keeps this file's dep graph tight — see historyView for the
// adapter that bridges over from the real history slice.
type providersMessageView struct{ content string }

// corpusBuilder accumulates strings up to a byte cap. add returns
// false when the cap is reached — caller stops feeding.
type corpusBuilder struct {
	parts []string
	used  int
	cap   int
}

func (b *corpusBuilder) add(s string) bool {
	if s == "" {
		return true
	}
	if b.used+len(s) > b.cap {
		// Truncate to fit the remaining budget if it's a useful
		// amount (>1KB); otherwise stop. Truncating to 100 bytes
		// is just noise.
		remaining := b.cap - b.used
		if remaining < 1024 {
			return false
		}
		b.parts = append(b.parts, s[:remaining])
		b.used = b.cap
		return false
	}
	b.parts = append(b.parts, s)
	b.used += len(s)
	return true
}

func (b *corpusBuilder) String() string {
	if len(b.parts) == 0 {
		return ""
	}
	// Joining with newline keeps grep-style substring matches inside
	// each piece intact — we don't want a corpus split mid-identifier.
	total := 0
	for _, p := range b.parts {
		total += len(p) + 1
	}
	out := make([]byte, 0, total)
	for i, p := range b.parts {
		if i > 0 {
			out = append(out, '\n')
		}
		out = append(out, p...)
	}
	return string(out)
}

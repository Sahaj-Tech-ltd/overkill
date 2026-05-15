package journal

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	times "time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

type Summarizer struct {
	recorder *FlightRecorder
	provider providers.Provider
	model    string
}

func NewSummarizer(recorder *FlightRecorder, provider providers.Provider, model string) *Summarizer {
	return &Summarizer{
		recorder: recorder,
		provider: provider,
		model:    model,
	}
}

func (s *Summarizer) Summarize(ctx context.Context, sessionID string) (string, error) {
	entries, err := s.recorder.ReadSession(sessionID)
	if err != nil {
		return "", fmt.Errorf("journal: reading session for summary: %w", err)
	}

	if len(entries) == 0 {
		return "", nil
	}

	return s.callLLM(ctx, entries)
}

func (s *Summarizer) SummarizeDay(ctx context.Context, date times.Time) (string, error) {
	entries, err := s.recorder.ReadDay(date)
	if err != nil {
		return "", fmt.Errorf("journal: reading day for summary: %w", err)
	}

	if len(entries) == 0 {
		return "", nil
	}

	return s.callLLM(ctx, entries)
}

// maxSummarizerEntries caps how many flight-recorder rows we feed the
// summariser LLM in one call. A busy day of agent activity easily hits
// tens of thousands of entries; concatenating them all blew past
// every model's context window and produced an obscure provider error
// rather than a usable summary. When over the cap, we keep the most
// recent N — late-day events are typically what the user wants
// remembered "what happened today" — and prepend a truncation note.
const maxSummarizerEntries = 800

func (s *Summarizer) callLLM(ctx context.Context, entries []Entry) (string, error) {
	truncated := false
	if len(entries) > maxSummarizerEntries {
		entries = entries[len(entries)-maxSummarizerEntries:]
		truncated = true
	}
	var sb strings.Builder
	if truncated {
		sb.WriteString(fmt.Sprintf("[truncated: showing last %d entries of a longer day]\n", maxSummarizerEntries))
	}
	for _, e := range entries {
		sb.WriteString(fmt.Sprintf("[%s] %s: %s\n", e.Timestamp.Format("15:04:05"), e.Type, e.Content))
	}

	resp, err := s.provider.Complete(ctx, providers.Request{
		Model: s.model,
		Messages: []providers.Message{
			{Role: "user", Content: sb.String()},
		},
		SystemPrompt: diaryNarrativePrompt,
	})
	if err != nil {
		return "", fmt.Errorf("journal: calling LLM for summary: %w", err)
	}

	return resp.Content, nil
}

// diaryNarrativePrompt produces the §4.19 sub-agent's structured
// diary output. The sub-agent is a tool-blocked observer: it reads
// the day's flight recorder and produces prose. It does NOT execute
// commands, write code, or spawn other agents — which is enforced
// at the model layer here by the explicit non-instructions, and at
// the runtime layer by the fact that we use the Summarizer's bare
// Complete() call (no tools registered).
const diaryNarrativePrompt = `You are Overkill's work-journal sub-agent.
You are reading raw flight-recorder entries from one day of coding
sessions. Your job: write a single short markdown diary entry the
user could read tomorrow to remember what happened. No code blocks,
no commands — just prose under headers.

Format the entry exactly:

# <Month/Day>

## What we did
One short paragraph. Concrete: which modules, what shipped.

## What we skipped or deferred
Bullets. Be honest — call out test coverage gaps, scope cuts, "we'll
fix this later" moments.

## What broke
Bullets. Errors that bit us, dead paths, hypotheses that didn't
work. Include the resolution if there was one.

## Friction
One line. Where the user got frustrated or where we lost time.

## Notes for tomorrow
Bullets. Open threads worth picking up. Skip this section if there
are none.

Rules:
- Tone: senior colleague keeping notes for themselves. No filler,
  no "great session!". The work is the entry.
- Be specific. Filenames, function names, error classes — not
  vague "we worked on stuff".
- If a section has nothing to say, write "(nothing notable)" rather
  than padding.
- Never invent details. If the journal doesn't show it, don't
  write it.`

func (s *Summarizer) WriteSummary(dir string, sessionID string, summary string) error {
	entriesDir := filepath.Join(dir, "entries")
	if err := os.MkdirAll(entriesDir, 0o755); err != nil {
		return fmt.Errorf("journal: creating entries dir: %w", err)
	}

	now := times.Now()
	filename := fmt.Sprintf("%s-%s.md", now.Format("2006-01-02"), sessionID)
	path := filepath.Join(entriesDir, filename)

	if err := os.WriteFile(path, []byte(summary), 0o644); err != nil {
		return fmt.Errorf("journal: writing summary: %w", err)
	}

	return nil
}

// WriteDayNarrative writes the day's diary entry to
// <dir>/entries/<YYYY-MM-DD>.md. When the file already exists
// (multiple sessions in one day), the new narrative is appended
// under a `## session <id>` sub-section so the day stays one file
// per the §4.19 spec while still preserving per-session attribution.
func (s *Summarizer) WriteDayNarrative(dir string, sessionID string, narrative string, when times.Time) error {
	entriesDir := filepath.Join(dir, "entries")
	if err := os.MkdirAll(entriesDir, 0o755); err != nil {
		return fmt.Errorf("journal: creating entries dir: %w", err)
	}
	path := filepath.Join(entriesDir, when.Format("2006-01-02")+".md")

	// Honor §4.19 "one file per day" by appending session-scoped
	// entries to an existing file rather than overwriting.
	if existing, err := os.ReadFile(path); err == nil && len(existing) > 0 {
		header := fmt.Sprintf("\n\n---\n\n## session %s (%s)\n\n",
			sessionID, when.Format("15:04"))
		combined := string(existing) + header + strings.TrimSpace(narrative) + "\n"
		return os.WriteFile(path, []byte(combined), 0o644)
	}
	if err := os.WriteFile(path, []byte(narrative), 0o644); err != nil {
		return fmt.Errorf("journal: writing day narrative: %w", err)
	}
	return nil
}

// NarrateSession runs the full diary-renderer pipeline for one
// session: read entries → produce narrative → persist to the day
// file. Returns the path written and the narrative text. Empty
// sessions (no journal entries) are a no-op — returns ("", "", nil).
//
// Intended call sites: TUI session-end defer (best-effort, errors
// logged), `overkill journal narrate` CLI (errors surfaced).
func (s *Summarizer) NarrateSession(ctx context.Context, dir, sessionID string) (string, string, error) {
	if sessionID == "" {
		return "", "", fmt.Errorf("journal: NarrateSession: empty session id")
	}
	entries, err := s.recorder.ReadSession(sessionID)
	if err != nil {
		// Missing raw directory is the "no journal yet" state, not
		// an error. The recorder lazy-creates raw/ on first write;
		// a session that ended without recording anything is a
		// valid no-op for the narrator.
		if os.IsNotExist(err) || strings.Contains(err.Error(), "no such file") {
			return "", "", nil
		}
		return "", "", fmt.Errorf("journal: read session %s: %w", sessionID, err)
	}
	if len(entries) == 0 {
		return "", "", nil
	}
	narrative, err := s.callLLM(ctx, entries)
	if err != nil {
		return "", "", err
	}
	when := times.Now().UTC()
	if last := entries[len(entries)-1].Timestamp; !last.IsZero() {
		when = last
	}
	if err := s.WriteDayNarrative(dir, sessionID, narrative, when); err != nil {
		return "", narrative, err
	}
	path := filepath.Join(dir, "entries", when.Format("2006-01-02")+".md")
	return path, narrative, nil
}

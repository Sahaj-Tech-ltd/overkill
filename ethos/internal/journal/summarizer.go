package journal

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	times "time"

	"github.com/Sahaj-Tech-ltd/ethos/internal/providers"
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

func (s *Summarizer) callLLM(ctx context.Context, entries []Entry) (string, error) {
	var sb strings.Builder
	for _, e := range entries {
		sb.WriteString(fmt.Sprintf("[%s] %s: %s\n", e.Timestamp.Format("15:04:05"), e.Type, e.Content))
	}

	resp, err := s.provider.Complete(ctx, providers.Request{
		Model: s.model,
		Messages: []providers.Message{
			{Role: "user", Content: sb.String()},
		},
		SystemPrompt: "You are a work journal writer. Summarize this coding session concisely. Include: what was done, what was skipped, what frustrated the user, what went wrong. Write like a diary entry. Date format: M/D. Be honest.",
	})
	if err != nil {
		return "", fmt.Errorf("journal: calling LLM for summary: %w", err)
	}

	return resp.Content, nil
}

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

// Package automemory provides post-turn memory extraction.
// After each completed query loop (final response, no tool calls),
// a forked agent extracts durable facts from the transcript and
// writes them to file-based memory (~/.overkill/memory/).
//
// Architecture ported from Claude Code's extractMemories.ts.
package automemory

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// memoryDir is the root for auto-extracted memory files.
const memoryDir = ".overkill/memory"

// Extractor manages the auto-memory lifecycle.
type Extractor struct {
	mu sync.Mutex

	// HomeDir is the user's home directory (expands memoryDir).
	HomeDir string

	// MinNewMessages is the minimum number of new model-visible
	// messages required before extraction fires. Prevents
	// extraction on every single-exchange interaction.
	MinNewMessages int

	// LastExtractedAt is the UUID of the last message that was
	// included in an extraction. Messages after this are candidates.
	lastExtractedAt string

	// ExtractFn is called to perform the actual extraction.
	// The callback receives the transcript text and returns
	// extracted facts + their categories.
	ExtractFn func(ctx context.Context, transcript string) ([]Fact, error)
}

// Fact is a single extracted memory entry.
type Fact struct {
	Content  string `json:"content"`
	Category string `json:"category"` // "user", "project", "conversation", "general"
}

// NewExtractor creates a memory extractor with defaults.
func NewExtractor(homeDir string) *Extractor {
	return &Extractor{
		HomeDir:        homeDir,
		MinNewMessages: 5,
	}
}

// ShouldExtract returns true if enough new messages have accumulated
// since the last extraction.
func (e *Extractor) ShouldExtract(newMessageCount int, latestMessageID string) bool {
	if e.ExtractFn == nil {
		return false
	}
	if newMessageCount < e.MinNewMessages {
		return false
	}
	return true
}

// MarkExtracted records that messages up to the given ID have been processed.
func (e *Extractor) MarkExtracted(lastMessageID string) {
	e.mu.Lock()
	e.lastExtractedAt = lastMessageID
	e.mu.Unlock()
}

// Extract runs the extraction pipeline and writes results to disk.
func (e *Extractor) Extract(ctx context.Context, transcript string) error {
	if e.ExtractFn == nil {
		return fmt.Errorf("automemory: ExtractFn not set")
	}

	facts, err := e.ExtractFn(ctx, transcript)
	if err != nil {
		return fmt.Errorf("automemory: extract: %w", err)
	}

	if len(facts) == 0 {
		return nil
	}

	return e.writeFacts(facts)
}

// writeFacts appends new facts to today's memory file, deduplicating
// against existing entries.
func (e *Extractor) writeFacts(facts []Fact) error {
	dir := filepath.Join(e.HomeDir, memoryDir)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return err
	}

	today := time.Now().Format("2006-01-02")
	path := filepath.Join(dir, today+".md")

	// Read existing entries for dedup.
	existing := readMemoryFile(path)

	// Determine if we need a header (new or empty file).
	needsHeader := false
	if info, err := os.Stat(path); err != nil || info.Size() == 0 {
		needsHeader = true
	}

	// Append new facts.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()

	if needsHeader {
		dateHeader := time.Now().Format("## Monday, January 2, 2006")
		if _, err := f.WriteString(dateHeader + "\n"); err != nil {
			return err
		}
	}

	written := 0
	for _, fact := range facts {
		if isDuplicate(existing, fact.Content) {
			continue
		}
		line := formatFact(fact)
		if _, err := f.WriteString(line + "\n"); err != nil {
			return err
		}
		written++
	}

	return nil
}

// readMemoryFile returns existing lines from a memory file.
func readMemoryFile(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return strings.Split(string(data), "\n")
}

// isDuplicate checks if content already exists (case-insensitive substring match).
// Stored facts are formatted as "- [category] content", so we check contains
// rather than exact equality.
func isDuplicate(existing []string, content string) bool {
	if content == "" {
		return false
	}
	normalized := strings.ToLower(strings.TrimSpace(content))
	for _, line := range existing {
		if strings.Contains(strings.ToLower(line), normalized) {
			return true
		}
	}
	return false
}

// formatFact formats a Fact as a markdown list item.
func formatFact(fact Fact) string {
	return fmt.Sprintf("- [%s] %s", fact.Category, fact.Content)
}

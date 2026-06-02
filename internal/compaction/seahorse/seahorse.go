// Package seahorse implements hierarchical DAG summarization — a more
// sophisticated alternative to flat LCM compaction. Stolen from PicoClaw's
// pkg/seahorse/.
//
// LCM is dual-state (immutable store + active context). Seahorse adds:
//   - Multi-level summary DAG (leaf at depth 0, condensed at depth N+1)
//   - Budget-aware context assembly with provider-safe boundary trimming
//   - Depth-aware system prompt injection
//   - FTS5 trigram search tools exposed to the LLM
//
// Storage: Postgres with FTS5-equivalent (Postgres tsvector/trigram).
package seahorse

import (
	"context"
	"fmt"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

// SummaryKind distinguishes leaf summaries (from raw messages) from
// condensed summaries (from other summaries).
type SummaryKind string

const (
	KindLeaf      SummaryKind = "leaf"
	KindCondensed SummaryKind = "condensed"
)

// Summary is one node in the hierarchical DAG. Leaf summaries are
// generated from raw messages (depth=0). Condensed summaries merge
// 4+ other summaries (depth = max(parent depths) + 1).
type Summary struct {
	SummaryID               string      `json:"summaryId"`
	Kind                    SummaryKind `json:"kind"`
	Depth                   int         `json:"depth"` // 0=from messages, 1+=from summaries
	Content                 string      `json:"content"`
	TokenCount              int         `json:"tokenCount"`
	DescendantCount         int         `json:"descendantCount"`      // recursive child count
	DescendantTokenCount    int         `json:"descendantTokenCount"` // recursive token sum
	SourceMessageTokenCount int         `json:"sourceMessageTokenCount"`
	Model                   string      `json:"model"` // which model produced this
	EarliestAt              time.Time   `json:"earliestAt"`
	LatestAt                time.Time   `json:"latestAt"`
	ParentIDs               []string    `json:"parentIds,omitempty"` // for condensed summaries
}

// ContextItem is one element in the assembled context window. Items
// have ordinals for stable ordering; ordinals can be resequenced when
// the gap between adjacent items shrinks too much.
type ContextItem struct {
	Ordinal    int    `json:"ordinal"`
	ItemType   string `json:"itemType"` // "summary" or "message"
	SummaryID  string `json:"summaryId,omitempty"`
	MessageID  int64  `json:"messageId,omitempty"`
	TokenCount int    `json:"tokenCount"`
}

// AssemblyResult is the output of budget-aware assembly: a slice of
// context items + the system prompt injection (if depth is significant).
type AssemblyResult struct {
	Items              []ContextItem `json:"items"`
	DepthWarning       string        `json:"depthWarning,omitempty"`
	TotalTokens        int           `json:"totalTokens"`
	EvictedCount       int           `json:"evictedCount"`
	FreshTailPreserved int           `json:"freshTailPreserved"`
}

// CompactOptions controls compaction behavior.
type CompactOptions struct {
	// FreshTailCount is the number of most recent messages that are
	// never compacted — they stay as raw messages so the LLM has
	// immediate context. Default: 32.
	FreshTailCount int

	// MinChunkMessages is the minimum number of contiguous messages
	// before a leaf compaction is triggered. Default: 8.
	MinChunkMessages int

	// MinChunkTokens triggers compaction when a chunk exceeds this
	// token count regardless of message count. Default: 20480.
	MinChunkTokens int

	// MinCondensedCount is the minimum number of summaries at the
	// same depth before condensed compaction triggers. Default: 4.
	MinCondensedCount int

	// LeafTargetTokens is the target token count for leaf summaries.
	// Default: 1200.
	LeafTargetTokens int

	// CondensedTargetTokens is the target token count for condensed
	// summaries. Default: 2000.
	CondensedTargetTokens int

	// MaxBudgetTokens is the hard ceiling for the assembled context.
	MaxBudgetTokens int

	// DepthWarningThreshold triggers the depth-aware system prompt
	// injection when max depth reaches this level. Default: 2.
	DepthWarningThreshold int
}

// DefaultCompactOptions returns sensible defaults.
func DefaultCompactOptions() CompactOptions {
	return CompactOptions{
		FreshTailCount:        32,
		MinChunkMessages:      8,
		MinChunkTokens:        20480,
		MinCondensedCount:     4,
		LeafTargetTokens:      1200,
		CondensedTargetTokens: 2000,
		MaxBudgetTokens:       200000,
		DepthWarningThreshold: 2,
	}
}

// Assembler builds budget-aware context windows from summaries and
// raw messages. It protects the FreshTail (most recent N messages)
// from eviction and ensures provider-safe boundaries.
type Assembler struct {
	opts CompactOptions
}

// NewAssembler creates an assembler with the given options.
func NewAssembler(opts CompactOptions) *Assembler {
	if opts.FreshTailCount == 0 {
		opts.FreshTailCount = 32
	}
	return &Assembler{opts: opts}
}

// Assemble builds the context window from items + summaries. It:
//  1. Splits items into evictable prefix + protected fresh tail suffix
//  2. Fits as many evictable items as budget allows (newest first)
//  3. If fresh tail alone exceeds budget, trims to safe boundaries
//  4. Injects depth warning if max depth exceeds threshold
func (a *Assembler) Assemble(items []ContextItem, summaries []Summary) *AssemblyResult {
	if a.opts.MaxBudgetTokens <= 0 {
		a.opts.MaxBudgetTokens = 200000
	}

	// Build depth map from summaries.
	depthBySummary := make(map[string]int, len(summaries))
	maxDepth := 0
	for _, s := range summaries {
		depthBySummary[s.SummaryID] = s.Depth
		if s.Depth > maxDepth {
			maxDepth = s.Depth
		}
	}

	// Split into prefix (evictable) and suffix (protected fresh tail).
	split := len(items) - a.opts.FreshTailCount
	if split < 0 {
		split = 0
	}
	evictable := items[:split]
	freshTail := items[split:]

	// Count fresh tail tokens.
	freshTokens := 0
	for _, it := range freshTail {
		freshTokens += it.TokenCount
	}

	// If fresh tail alone exceeds budget, trim to safe boundaries.
	if freshTokens > a.opts.MaxBudgetTokens {
		trimmed := a.trimFreshTail(freshTail, a.opts.MaxBudgetTokens)
		return &AssemblyResult{
			Items:              trimmed,
			FreshTailPreserved: len(trimmed),
			TotalTokens:        sumTokens(trimmed),
			EvictedCount:       len(items) - len(trimmed),
		}
	}

	// Fit evictable items — newest first.
	remaining := a.opts.MaxBudgetTokens - freshTokens
	kept := make([]ContextItem, 0, len(evictable))
	for i := len(evictable) - 1; i >= 0; i-- {
		it := evictable[i]
		if it.TokenCount <= remaining {
			kept = append(kept, it)
			remaining -= it.TokenCount
		}
	}
	// Reverse kept back to chronological order.
	for i, j := 0, len(kept)-1; i < j; i, j = i+1, j-1 {
		kept[i], kept[j] = kept[j], kept[i]
	}

	result := append(kept, freshTail...)
	evicted := len(evictable) - len(kept)

	out := &AssemblyResult{
		Items:              result,
		FreshTailPreserved: len(freshTail),
		TotalTokens:        sumTokens(result),
		EvictedCount:       evicted,
	}

	// Depth warning.
	if maxDepth >= a.opts.DepthWarningThreshold {
		condensedCount := 0
		for _, it := range result {
			if it.ItemType == "summary" {
				if d, ok := depthBySummary[it.SummaryID]; ok && d > 0 {
					condensedCount++
				}
			}
		}
		if maxDepth >= 2 || condensedCount >= 2 {
			out.DepthWarning = fmt.Sprintf(
				"Your context has been heavily compressed through %d-level summarization (%d condensed summaries). "+
					"Do NOT assert specific facts from summaries without expanding. "+
					"Tool escalation: grep → describe → expand.",
				maxDepth, condensedCount,
			)
		}
	}

	return out
}

// trimFreshTail removes items from the start of freshTail until it fits
// within budget. Ensures provider-safe boundaries: won't split in the
// middle of a tool call/result pair (consecutive messages with adjacent
// MessageIDs).
func (a *Assembler) trimFreshTail(freshTail []ContextItem, budget int) []ContextItem {
	// Walk from the end (newest) backwards, keeping items until budget
	// is exceeded, then drop everything older.
	kept := make([]ContextItem, 0, len(freshTail))
	used := 0
	cutIdx := len(freshTail) // index of first dropped item (oldest among kept)
	for i := len(freshTail) - 1; i >= 0; i-- {
		it := freshTail[i]
		if used+it.TokenCount <= budget {
			kept = append(kept, it)
			used += it.TokenCount
			cutIdx = i
		} else {
			break
		}
	}

	// Ensure provider-safe boundaries: if the cut point splits a
	// tool-call/result pair (two consecutive "message" items with
	// adjacent MessageIDs), keep the older item too so the pair
	// stays intact.
	if cutIdx > 0 && cutIdx < len(freshTail) {
		older := freshTail[cutIdx-1]
		newer := freshTail[cutIdx]
		if older.ItemType == "message" && newer.ItemType == "message" &&
			newer.MessageID == older.MessageID+1 {
			// These are consecutive messages — likely a tool-call/result pair.
			// Include the older item to keep the pair together.
			kept = append(kept, older)
		}
	}

	// Reverse to chronological order.
	for i, j := 0, len(kept)-1; i < j; i, j = i+1, j-1 {
		kept[i], kept[j] = kept[j], kept[i]
	}
	return kept
}

// XML formats a summary for LLM consumption — the structured format
// makes it easier for models to understand multi-level compression.
func (s *Summary) XML() string {
	kind := string(s.Kind)
	out := fmt.Sprintf(
		`<summary id="%s" kind="%s" depth="%d" descendant_count="%d" earliest_at="%s" latest_at="%s">`,
		s.SummaryID, kind, s.Depth, s.DescendantCount,
		s.EarliestAt.Format(time.RFC3339), s.LatestAt.Format(time.RFC3339),
	)
	out += "\n  <content>" + s.Content + "</content>"
	if len(s.ParentIDs) > 0 {
		out += "\n  <parents>"
		for _, pid := range s.ParentIDs {
			out += fmt.Sprintf("\n    <summary_ref id=\"%s\" />", pid)
		}
		out += "\n  </parents>"
	}
	out += "\n</summary>"
	return out
}

// ── helpers ─────────────────────────────────────────────────────────

func sumTokens(items []ContextItem) int {
	total := 0
	for _, it := range items {
		total += it.TokenCount
	}
	return total
}

// Compactor is the interface for producing summaries. Implementations
// can be LLM-based (primary) or heuristic (fallback).
type Compactor interface {
	CompactLeaf(ctx context.Context, messages []providers.Message) (*Summary, error)
	CompactCondensed(ctx context.Context, summaries []Summary) (*Summary, error)
}

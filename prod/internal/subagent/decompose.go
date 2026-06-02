package subagent

import "strings"

// Decomposer splits a natural-language multi-item goal into discrete tasks.
// Uses separator patterns and heuristics — no LLM call. When a user dumps
// "fix X in package A, also wire Y in package B, and then refactor Z" this
// extracts the individual items so they can be dispatched as parallel
// sub-agents instead of overwhelming one with a giant context window.
//
// This is intentionally self-contained in the subagent package (no dependency
// on internal/agent) so it can be called from the delegation path without
// creating a circular import.
type Decomposer struct {
	separators []string
	minItemLen int
	maxItems   int
}

// NewDecomposer returns a Decomposer with sensible defaults.
func NewDecomposer() *Decomposer {
	return &Decomposer{
		separators: []string{
			"\n- ", "\n• ", "\n* ",
			"\n1. ", "\n2. ", "\n3. ", "\n4. ", "\n5. ",
			"\nand ", "\nthen ", "\nalso ", "\nplus ",
			". also ", ". then ", ". next ", ". plus ",
			"; also ", "; then ", "; next ",
		},
		minItemLen: 10,
		maxItems:   10,
	}
}

// Decompose splits input into discrete task descriptions.
// Returns nil if fewer than 2 items are found (single-item input
// doesn't need decomposition).
func (d *Decomposer) Decompose(input string) []string {
	normalized := strings.ReplaceAll(input, "\r\n", "\n")
	prefixed := "\n" + normalized

	var bestItems []string
	bestCount := 0

	for _, sep := range d.separators {
		parts := strings.Split(prefixed, sep)
		if len(parts) < 2 {
			continue
		}
		var items []string
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if len(part) >= d.minItemLen {
				items = append(items, part)
			}
		}
		if len(items) > bestCount {
			bestItems = items
			bestCount = len(items)
		}
	}

	if len(bestItems) < 2 {
		return nil
	}
	if len(bestItems) > d.maxItems {
		bestItems = bestItems[:d.maxItems]
	}
	return bestItems
}

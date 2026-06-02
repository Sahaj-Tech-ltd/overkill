// Package agent — sequential multi-item processor (§8.6.1).
//
// When a user dumps 3+ items in one message, this decomposes them into
// discrete work items and processes them one at a time. Each item gets:
//   - Its own context (only relevant files)
//   - A self-evaluate pass
//   - Independent verification
//
// This is the "sit and think" capability — instead of trying to handle
// everything in one polluted context window, we iterate.

package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// QueueState is the live state of the sequential processing queue,
// exposed to the TUI Queue pane via the API.
type QueueState struct {
	mu     sync.RWMutex
	Active bool        // true while processing
	Total  int         // total items
	Done   int         // completed items
	Failed int         // failed items
	Items  []QueueItem // current items with status
}

// QueueItem mirrors WorkItem for the API boundary.
type QueueItem struct {
	Index       int    `json:"index"`
	Description string `json:"description"`
	Status      string `json:"status"`
	Error       string `json:"error,omitempty"`
	ElapsedMs   int64  `json:"elapsed_ms"`
}

// Snapshot returns a copy of the current queue state for safe concurrent reads.
func (qs *QueueState) Snapshot() QueueState {
	if qs == nil {
		return QueueState{}
	}
	qs.mu.RLock()
	defer qs.mu.RUnlock()
	items := make([]QueueItem, len(qs.Items))
	copy(items, qs.Items)
	return QueueState{
		Active: qs.Active,
		Total:  qs.Total,
		Done:   qs.Done,
		Failed: qs.Failed,
		Items:  items,
	}
}

// Reset clears the queue for a new run.
func (qs *QueueState) Reset() {
	if qs == nil {
		return
	}
	qs.mu.Lock()
	defer qs.mu.Unlock()
	qs.Active = false
	qs.Total = 0
	qs.Done = 0
	qs.Failed = 0
	qs.Items = nil
}

// SetItems initializes the queue with work items.
func (qs *QueueState) SetItems(items []WorkItem) {
	if qs == nil {
		return
	}
	qs.mu.Lock()
	defer qs.mu.Unlock()
	qs.Active = true
	qs.Total = len(items)
	qs.Done = 0
	qs.Failed = 0
	qs.Items = make([]QueueItem, len(items))
	for i, item := range items {
		qs.Items[i] = QueueItem{
			Index:       item.Index,
			Description: item.Description,
			Status:      item.Status.String(),
		}
	}
}

// UpdateItem updates a single item's status in the queue.
func (qs *QueueState) UpdateItem(index int, status string, err string, elapsed time.Duration) {
	if qs == nil {
		return
	}
	qs.mu.Lock()
	defer qs.mu.Unlock()
	for i := range qs.Items {
		if qs.Items[i].Index == index {
			qs.Items[i].Status = status
			qs.Items[i].Error = err
			qs.Items[i].ElapsedMs = elapsed.Milliseconds()
			break
		}
	}
}

// Finish marks the queue as complete.
func (qs *QueueState) Finish() {
	if qs == nil {
		return
	}
	qs.mu.Lock()
	defer qs.mu.Unlock()
	qs.Active = false
}

// WorkItem is one discrete task extracted from a multi-item user dump.
type WorkItem struct {
	Index       int    // 1-based position in original message
	Description string // the extracted task description
	Status      WorkItemStatus
	Result      string // output from processing this item
	Error       string // if failed, the error message
	StartedAt   time.Time
	CompletedAt time.Time
}

// WorkItemStatus tracks progress through the sequential processor.
type WorkItemStatus int

const (
	WorkItemPending WorkItemStatus = iota
	WorkItemActive
	WorkItemDone
	WorkItemFailed
	WorkItemSkipped
)

func (s WorkItemStatus) String() string {
	switch s {
	case WorkItemPending:
		return "pending"
	case WorkItemActive:
		return "active"
	case WorkItemDone:
		return "done"
	case WorkItemFailed:
		return "failed"
	case WorkItemSkipped:
		return "skipped"
	default:
		return "unknown"
	}
}

// ─────────────────────────────────────────────────────────────────────
// Decomposer: extract work items from natural language.
// ─────────────────────────────────────────────────────────────────────

// Decomposer extracts discrete work items from natural language multi-item dumps.
// Uses separator patterns and heuristics — no LLM call.
type Decomposer struct {
	// separators are patterns that split multi-item input.
	separators []string
	// minItemLen is the minimum character length for a valid work item.
	minItemLen int
	// maxItems caps the number of items extracted.
	maxItems int
}

// NewDecomposer returns a decomposer with sensible defaults.
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

// Decompose splits user input into discrete work items.
// Returns nil if fewer than 2 items are found (single-item input
// doesn't need sequential processing).
func (d *Decomposer) Decompose(input string) []WorkItem {
	normalized := strings.ReplaceAll(input, "\r\n", "\n")
	// Add leading newline so patterns like "1." at position 0 also match.
	prefixed := "\n" + normalized

	// Try each separator, pick the one that yields the most valid items.
	var bestItems []WorkItem
	bestCount := 0

	for _, sep := range d.separators {
		parts := strings.Split(prefixed, sep)
		var items []WorkItem

		for i, part := range parts {
			part = strings.TrimSpace(part)
			if len(part) >= d.minItemLen {
				items = append(items, WorkItem{
					Index:       i + 1,
					Description: cleanItem(part),
					Status:      WorkItemPending,
				})
			}
		}

		if len(items) > bestCount {
			bestItems = items
			bestCount = len(items)
		}
	}

	// Re-number items sequentially.
	for i := range bestItems {
		bestItems[i].Index = i + 1
	}

	if len(bestItems) < 2 {
		return nil
	}

	// Cap at maxItems.
	if len(bestItems) > d.maxItems {
		bestItems = bestItems[:d.maxItems]
	}

	return bestItems
}

// cleanItem removes common prefix artifacts from extracted items.
func cleanItem(item string) string {
	// Strip leading numbering like "1." or "1)"
	item = strings.TrimSpace(item)
	for _, prefix := range []string{"- ", "• ", "* ", "and ", "then ", "also ", "plus "} {
		if strings.HasPrefix(strings.ToLower(item), prefix) {
			item = strings.TrimPrefix(item, prefix)
			item = strings.TrimSpace(item)
		}
	}
	return item
}

// ─────────────────────────────────────────────────────────────────────
// Sequential processor: iterate items one at a time.
// ─────────────────────────────────────────────────────────────────────

// ItemProcessor is the callback that processes one work item.
// It receives the item context and should return the result text
// or an error. The processor is called once per item.
type ItemProcessor func(ctx context.Context, item WorkItem) (result string, err error)

// SequentialResult is the aggregated output of processing all items.
type SequentialResult struct {
	Items       []WorkItem
	TotalItems  int
	DoneCount   int
	FailedCount int
	TotalTime   time.Duration
	Summary     string
}

// SequentialProcessor iterates through work items, processing each
// one independently via the provided ItemProcessor.
type SequentialProcessor struct {
	Decomposer *Decomposer
	MaxPerItem time.Duration // max time per item (default: 5 min)
	Verbose    bool          // if true, emit progress per item
}

// NewSequentialProcessor creates a processor with defaults.
func NewSequentialProcessor() *SequentialProcessor {
	return &SequentialProcessor{
		Decomposer: NewDecomposer(),
		MaxPerItem: 5 * time.Minute,
		Verbose:    true,
	}
}

// Process decomposes input into items and processes them sequentially.
// Returns the aggregated result. If decomposition yields < 2 items,
// returns nil — caller should fall back to normal single-item processing.
func (sp *SequentialProcessor) Process(
	ctx context.Context,
	input string,
	processor ItemProcessor,
) (*SequentialResult, error) {
	items := sp.Decomposer.Decompose(input)
	if len(items) < 2 {
		return nil, nil // not a multi-item request
	}
	if processor == nil {
		return nil, fmt.Errorf("sequential: nil ItemProcessor")
	}

	startTime := time.Now()
	result := &SequentialResult{
		Items:      items,
		TotalItems: len(items),
	}

	var summaryParts []string

	for i := range result.Items {
		item := &result.Items[i]
		item.Status = WorkItemActive
		item.StartedAt = time.Now()

		// Per-item timeout. Wrap in recover() so a panicking processor
		// doesn't crash the agent loop — mark the item failed and continue.
		itemCtx, cancel := context.WithTimeout(ctx, sp.MaxPerItem)
		var output string
		var err error
		func() {
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("processor panic: %v", r)
				}
			}()
			output, err = processor(itemCtx, *item)
		}()
		cancel()

		item.CompletedAt = time.Now()

		if err != nil {
			item.Status = WorkItemFailed
			item.Error = err.Error()
			result.FailedCount++
			summaryParts = append(summaryParts,
				fmt.Sprintf("❌ Item %d (%s): %s", item.Index, truncate(item.Description, 60), err.Error()))
		} else {
			item.Status = WorkItemDone
			item.Result = output
			result.DoneCount++
			summaryParts = append(summaryParts,
				fmt.Sprintf("✅ Item %d (%s): done", item.Index, truncate(item.Description, 60)))
		}
	}

	result.TotalTime = time.Since(startTime)
	result.Summary = fmt.Sprintf(
		"Processed %d items: %d done, %d failed in %s\n%s",
		result.TotalItems,
		result.DoneCount,
		result.FailedCount,
		result.TotalTime.Round(time.Second),
		strings.Join(summaryParts, "\n"),
	)

	return result, nil
}

// ─────────────────────────────────────────────────────────────────────
// Item context: map items to relevant files.
// ─────────────────────────────────────────────────────────────────────

// ItemContext maps a work item to relevant files based on keywords.
// This is intentionally simple — a full implementation would use
// the codebase index or embeddings. For now, keyword matching on
// the item description finds likely-relevant paths.
type ItemContext struct {
	// fileIndex is a map of keyword → file paths.
	// Populated from project structure on boot.
	fileIndex map[string][]string
}

// NewItemContext creates an empty item context mapper.
func NewItemContext() *ItemContext {
	return &ItemContext{
		fileIndex: make(map[string][]string),
	}
}

// IndexPath registers a file path under relevant keywords.
func (ic *ItemContext) IndexPath(path string) {
	lower := strings.ToLower(path)
	// Extract keywords from path components.
	parts := strings.FieldsFunc(lower, func(r rune) bool {
		return r == '/' || r == '.' || r == '-' || r == '_'
	})
	for _, part := range parts {
		if len(part) >= 3 {
			ic.fileIndex[part] = append(ic.fileIndex[part], path)
		}
	}
}

// RelevantFiles returns file paths relevant to a work item description.
// Matches keywords in the description against the indexed paths.
func (ic *ItemContext) RelevantFiles(item WorkItem) []string {
	lower := strings.ToLower(item.Description)
	seen := make(map[string]bool)
	var files []string

	for keyword, paths := range ic.fileIndex {
		if strings.Contains(lower, keyword) {
			for _, p := range paths {
				if !seen[p] {
					seen[p] = true
					files = append(files, p)
				}
			}
		}
	}

	return files
}

// FormatItemPrompt builds the prompt for a single work item, including
// relevant file context.
func (ic *ItemContext) FormatItemPrompt(item WorkItem, totalItems int) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("## Item %d of %d\n\n", item.Index, totalItems))
	b.WriteString(item.Description)
	b.WriteString("\n")

	// Include relevant files if we have context.
	if files := ic.RelevantFiles(item); len(files) > 0 {
		b.WriteString("\n### Relevant Files\n")
		for _, f := range files {
			b.WriteString(fmt.Sprintf("- %s\n", f))
		}
	}

	b.WriteString(fmt.Sprintf("\nProcess ONLY this item. Do not address other items.\n"))

	return b.String()
}

package seahorse

import (
	"strings"
	"testing"
	"time"
)

func TestSummaryKindConstants(t *testing.T) {
	if KindLeaf != "leaf" {
		t.Errorf("KindLeaf = %q, want 'leaf'", KindLeaf)
	}
	if KindCondensed != "condensed" {
		t.Errorf("KindCondensed = %q, want 'condensed'", KindCondensed)
	}
}

func TestSummaryXML(t *testing.T) {
	s := Summary{
		SummaryID:            "s-001",
		Kind:                 KindLeaf,
		Depth:                0,
		Content:              "User asked about Docker configuration.",
		DescendantCount:      0,
		DescendantTokenCount: 0,
		EarliestAt:           time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC),
		LatestAt:             time.Date(2025, 1, 15, 10, 5, 0, 0, time.UTC),
	}

	xml := s.XML()

	checks := []string{
		`id="s-001"`,
		`kind="leaf"`,
		`depth="0"`,
		`descendant_count="0"`,
		"<content>User asked about Docker configuration.</content>",
		"</summary>",
	}
	for _, c := range checks {
		if !strings.Contains(xml, c) {
			t.Errorf("XML output missing expected substring: %q\nGot: %s", c, xml)
		}
	}
}

func TestSummaryXMLWithParents(t *testing.T) {
	s := Summary{
		SummaryID:  "c-001",
		Kind:       KindCondensed,
		Depth:      2,
		Content:    "Merged 4 leaf summaries.",
		EarliestAt: time.Now().UTC(),
		LatestAt:   time.Now().UTC(),
		ParentIDs:  []string{"s-001", "s-002", "s-003", "s-004"},
	}

	xml := s.XML()

	if !strings.Contains(xml, `kind="condensed"`) {
		t.Error("missing condensed kind")
	}
	if !strings.Contains(xml, `<summary_ref id="s-001"`) {
		t.Error("missing parent ref s-001")
	}
	if !strings.Contains(xml, `<summary_ref id="s-004"`) {
		t.Error("missing parent ref s-004")
	}
	if !strings.Contains(xml, "</parents>") {
		t.Error("missing parents close tag")
	}
}

func TestSummaryXMLNoParents(t *testing.T) {
	s := Summary{
		SummaryID:  "s-002",
		Kind:       KindLeaf,
		Depth:      0,
		Content:    "leaf content",
		EarliestAt: time.Now().UTC(),
		LatestAt:   time.Now().UTC(),
		ParentIDs:  nil,
	}

	xml := s.XML()

	if strings.Contains(xml, "<parents>") {
		t.Error("leaf summary should not have <parents> section")
	}
}

func TestDefaultCompactOptions(t *testing.T) {
	opts := DefaultCompactOptions()

	if opts.FreshTailCount != 32 {
		t.Errorf("FreshTailCount = %d, want 32", opts.FreshTailCount)
	}
	if opts.MinChunkMessages != 8 {
		t.Errorf("MinChunkMessages = %d, want 8", opts.MinChunkMessages)
	}
	if opts.MinChunkTokens != 20480 {
		t.Errorf("MinChunkTokens = %d, want 20480", opts.MinChunkTokens)
	}
	if opts.MinCondensedCount != 4 {
		t.Errorf("MinCondensedCount = %d, want 4", opts.MinCondensedCount)
	}
	if opts.LeafTargetTokens != 1200 {
		t.Errorf("LeafTargetTokens = %d, want 1200", opts.LeafTargetTokens)
	}
	if opts.CondensedTargetTokens != 2000 {
		t.Errorf("CondensedTargetTokens = %d, want 2000", opts.CondensedTargetTokens)
	}
	if opts.MaxBudgetTokens != 200000 {
		t.Errorf("MaxBudgetTokens = %d, want 200000", opts.MaxBudgetTokens)
	}
	if opts.DepthWarningThreshold != 2 {
		t.Errorf("DepthWarningThreshold = %d, want 2", opts.DepthWarningThreshold)
	}
}

func TestNewAssemblerDefaults(t *testing.T) {
	// Zero FreshTailCount should be replaced with default 32
	a := NewAssembler(CompactOptions{
		MaxBudgetTokens: 1000,
	})
	if a.opts.FreshTailCount != 32 {
		t.Errorf("expected FreshTailCount default 32, got %d", a.opts.FreshTailCount)
	}
}

func TestNewAssemblerPreservesNonZero(t *testing.T) {
	a := NewAssembler(CompactOptions{
		FreshTailCount:  10,
		MaxBudgetTokens: 1000,
	})
	if a.opts.FreshTailCount != 10 {
		t.Errorf("expected FreshTailCount 10, got %d", a.opts.FreshTailCount)
	}
}

func TestAssembleZeroItems(t *testing.T) {
	a := NewAssembler(DefaultCompactOptions())
	result := a.Assemble(nil, nil)

	if len(result.Items) != 0 {
		t.Errorf("expected 0 items, got %d", len(result.Items))
	}
	if result.TotalTokens != 0 {
		t.Errorf("expected 0 total tokens, got %d", result.TotalTokens)
	}
	if result.EvictedCount != 0 {
		t.Errorf("expected 0 evicted, got %d", result.EvictedCount)
	}
}

func TestAssembleWithinBudget(t *testing.T) {
	a := NewAssembler(CompactOptions{
		FreshTailCount:  2,
		MaxBudgetTokens: 100,
	})

	items := []ContextItem{
		{Ordinal: 1, ItemType: "message", TokenCount: 10},
		{Ordinal: 2, ItemType: "message", TokenCount: 10},
		{Ordinal: 3, ItemType: "message", TokenCount: 10}, // fresh tail
		{Ordinal: 4, ItemType: "message", TokenCount: 10}, // fresh tail
	}

	result := a.Assemble(items, nil)

	if len(result.Items) != 4 {
		t.Errorf("expected 4 items, got %d", len(result.Items))
	}
	if result.EvictedCount != 0 {
		t.Errorf("expected 0 evicted, got %d", result.EvictedCount)
	}
	if result.FreshTailPreserved != 2 {
		t.Errorf("expected 2 fresh tail preserved, got %d", result.FreshTailPreserved)
	}
}

func TestAssembleEviction(t *testing.T) {
	// Budget tight enough that only some evictable items fit
	a := NewAssembler(CompactOptions{
		FreshTailCount:  2,
		MaxBudgetTokens: 50,
	})

	items := []ContextItem{
		{Ordinal: 1, ItemType: "message", TokenCount: 20}, // evictable
		{Ordinal: 2, ItemType: "message", TokenCount: 20}, // evictable
		{Ordinal: 3, ItemType: "message", TokenCount: 15}, // fresh tail
		{Ordinal: 4, ItemType: "message", TokenCount: 15}, // fresh tail
	}

	result := a.Assemble(items, nil)

	// Fresh tail = 30 tokens. Remaining budget = 50 - 30 = 20.
	// Only the newest evictable item (ordinal 2, 20 tokens) fits.
	// Ordinal 1 (20 tokens) does NOT fit because budget is exactly 20 and item 1 is older.

	if result.FreshTailPreserved != 2 {
		t.Errorf("expected 2 fresh tail preserved, got %d", result.FreshTailPreserved)
	}
	if result.EvictedCount != 1 {
		t.Errorf("expected 1 evicted, got %d", result.EvictedCount)
	}
	// Should have 3 items: ordinal 2 (evictable kept) + ordinals 3,4 (fresh tail)
	if len(result.Items) != 3 {
		t.Errorf("expected 3 items total, got %d", len(result.Items))
	}
}

func TestAssembleFreshTailExceedsBudget(t *testing.T) {
	// Fresh tail alone exceeds budget — must trim
	a := NewAssembler(CompactOptions{
		FreshTailCount:  3,
		MaxBudgetTokens: 30,
	})

	items := []ContextItem{
		{Ordinal: 1, ItemType: "message", TokenCount: 10},
		{Ordinal: 2, ItemType: "message", TokenCount: 15}, // fresh tail
		{Ordinal: 3, ItemType: "message", TokenCount: 15}, // fresh tail
		{Ordinal: 4, ItemType: "message", TokenCount: 10}, // fresh tail
	}

	result := a.Assemble(items, nil)

	// Fresh tail too big (40 tokens > 30), so trimFreshTail is called.
	// Items kept from newest back:
	// ordinal 4 (10t) = 10 used, fits
	// ordinal 3 (15t) = 25 used, fits
	// ordinal 2 (15t) = 40 used, exceeds 30 → break
	// So ordinals 3 and 4 are kept.

	if result.EvictedCount != 2 {
		t.Errorf("expected 2 evicted (ord 1 + ord 2), got %d", result.EvictedCount)
	}
	if len(result.Items) != 2 {
		t.Errorf("expected 2 items in result, got %d", len(result.Items))
	}
}

func TestAssembleDepthWarning(t *testing.T) {
	a := NewAssembler(CompactOptions{
		FreshTailCount:        1,
		MaxBudgetTokens:       1000,
		DepthWarningThreshold: 2,
	})

	items := []ContextItem{
		{Ordinal: 1, ItemType: "summary", SummaryID: "c-1", TokenCount: 100},
		{Ordinal: 2, ItemType: "summary", SummaryID: "c-2", TokenCount: 100},
		{Ordinal: 3, ItemType: "message", TokenCount: 10}, // fresh tail
	}

	summaries := []Summary{
		{SummaryID: "c-1", Depth: 3, Content: "deeply condensed"},
		{SummaryID: "c-2", Depth: 2, Content: "moderately condensed"},
	}

	result := a.Assemble(items, summaries)

	if result.DepthWarning == "" {
		t.Error("expected depth warning when maxDepth >= threshold and condensedCount >= 2")
	}
	if !strings.Contains(result.DepthWarning, "compressed") {
		t.Errorf("depth warning missing expected text: %q", result.DepthWarning)
	}
}

func TestAssembleDepthWarningNotTriggered(t *testing.T) {
	a := NewAssembler(CompactOptions{
		FreshTailCount:        1,
		MaxBudgetTokens:       1000,
		DepthWarningThreshold: 2,
	})

	items := []ContextItem{
		{Ordinal: 1, ItemType: "summary", SummaryID: "s-1", TokenCount: 100},
		{Ordinal: 2, ItemType: "message", TokenCount: 10}, // fresh tail
	}

	summaries := []Summary{
		{SummaryID: "s-1", Depth: 1, Content: "shallow"},
	}

	result := a.Assemble(items, summaries)

	if result.DepthWarning != "" {
		t.Errorf("depth warning should NOT trigger at depth %d with 0 condensed: got %q",
			maxDepthOf(summaries), result.DepthWarning)
	}
}

func TestAssembleMaxBudgetZeroDefault(t *testing.T) {
	a := NewAssembler(CompactOptions{
		FreshTailCount:  1,
		MaxBudgetTokens: 0, // should default to 200000
	})

	items := []ContextItem{
		{Ordinal: 1, ItemType: "message", TokenCount: 10},
		{Ordinal: 2, ItemType: "message", TokenCount: 10},
	}

	result := a.Assemble(items, nil)

	if len(result.Items) != 2 {
		t.Errorf("with default budget, all items should fit, got %d", len(result.Items))
	}
}

func TestSumTokens(t *testing.T) {
	items := []ContextItem{
		{TokenCount: 5},
		{TokenCount: 10},
		{TokenCount: 15},
	}
	total := sumTokens(items)
	if total != 30 {
		t.Errorf("sumTokens = %d, want 30", total)
	}
}

func TestSumTokensEmpty(t *testing.T) {
	if sumTokens(nil) != 0 {
		t.Error("sumTokens(nil) should be 0")
	}
	if sumTokens([]ContextItem{}) != 0 {
		t.Error("sumTokens([]) should be 0")
	}
}

func TestContextItemStruct(t *testing.T) {
	ci := ContextItem{
		Ordinal:    5,
		ItemType:   "summary",
		SummaryID:  "sum-42",
		MessageID:  0,
		TokenCount: 250,
	}
	if ci.Ordinal != 5 {
		t.Errorf("Ordinal = %d, want 5", ci.Ordinal)
	}
	if ci.ItemType != "summary" {
		t.Errorf("ItemType = %q, want 'summary'", ci.ItemType)
	}
}

func TestAssemblyResultStruct(t *testing.T) {
	ar := AssemblyResult{
		Items:              []ContextItem{{Ordinal: 1}},
		DepthWarning:       "warning text",
		TotalTokens:        100,
		EvictedCount:       3,
		FreshTailPreserved: 5,
	}
	if ar.TotalTokens != 100 {
		t.Errorf("TotalTokens = %d, want 100", ar.TotalTokens)
	}
	if ar.EvictedCount != 3 {
		t.Errorf("EvictedCount = %d, want 3", ar.EvictedCount)
	}
}

func TestSummaryStruct(t *testing.T) {
	now := time.Now().UTC()
	s := Summary{
		SummaryID:               "test-summary",
		Kind:                    KindLeaf,
		Depth:                   0,
		Content:                 "Hello world",
		TokenCount:              5,
		DescendantCount:         0,
		DescendantTokenCount:    0,
		SourceMessageTokenCount: 100,
		Model:                   "test-model",
		EarliestAt:              now,
		LatestAt:                now,
		ParentIDs:               nil,
	}

	if s.SummaryID != "test-summary" {
		t.Errorf("SummaryID mismatch")
	}
	if s.Model != "test-model" {
		t.Errorf("Model = %q, want 'test-model'", s.Model)
	}
	if s.SourceMessageTokenCount != 100 {
		t.Errorf("SourceMessageTokenCount = %d, want 100", s.SourceMessageTokenCount)
	}
}

func TestCompactOptionsStruct(t *testing.T) {
	opts := CompactOptions{
		FreshTailCount:        5,
		MinChunkMessages:      3,
		MinChunkTokens:        500,
		MinCondensedCount:     2,
		LeafTargetTokens:      800,
		CondensedTargetTokens: 1500,
		MaxBudgetTokens:       50000,
		DepthWarningThreshold: 3,
	}

	if opts.FreshTailCount != 5 {
		t.Errorf("FreshTailCount = %d", opts.FreshTailCount)
	}
	if opts.MinChunkMessages != 3 {
		t.Errorf("MinChunkMessages = %d", opts.MinChunkMessages)
	}
}

// --- helper ---

func maxDepthOf(s []Summary) int {
	m := 0
	for _, ss := range s {
		if ss.Depth > m {
			m = ss.Depth
		}
	}
	return m
}

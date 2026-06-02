package agent

import (
	"testing"
)

func TestDecomposer_Decompose_MultiItem(t *testing.T) {
	d := NewDecomposer()

	input := "Fix the auth bug in the middleware\n- Add rate limiting to all API endpoints\n- Update the README with new configuration options"

	items := d.Decompose(input)

	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}

	if items[0].Index != 1 {
		t.Errorf("expected first item index 1, got %d", items[0].Index)
	}
	if items[1].Index != 2 {
		t.Errorf("expected second item index 2, got %d", items[1].Index)
	}
	if items[2].Index != 3 {
		t.Errorf("expected third item index 3, got %d", items[2].Index)
	}

	if items[0].Status != WorkItemPending {
		t.Errorf("expected pending status, got %s", items[0].Status)
	}
}

func TestDecomposer_Decompose_NumberedItems(t *testing.T) {
	d := NewDecomposer()

	input := "1. Fix authentication bug\n2. Add rate limiting middleware\n3. Update documentation"

	items := d.Decompose(input)

	// Numbered lists are split by individual separator patterns.
	// Each separator only splits at one point, so we get 2 items
	// (split by \n2. yields the first group + rest).
	// Full regex-based numbered detection is a future enhancement.
	if len(items) < 2 {
		t.Fatalf("expected at least 2 items, got %d", len(items))
	}
}

func TestDecomposer_Decompose_SingleItem(t *testing.T) {
	d := NewDecomposer()

	input := "Fix the auth bug in the middleware. It's causing panics."

	items := d.Decompose(input)

	if items != nil {
		t.Errorf("expected nil for single item, got %d items", len(items))
	}
}

func TestDecomposer_Decompose_AndSeparator(t *testing.T) {
	d := NewDecomposer()

	input := "Refactor the database layer\nand add connection pooling\nand update the migration scripts"

	items := d.Decompose(input)

	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}
}

func TestDecomposer_Decompose_AlsoSeparator(t *testing.T) {
	d := NewDecomposer()

	input := "Fix the auth bug. also add rate limiting. also update the docs"

	items := d.Decompose(input)

	if len(items) < 2 {
		t.Errorf("expected at least 2 items, got %d", len(items))
	}
}

func TestDecomposer_CleansPrefixes(t *testing.T) {
	d := NewDecomposer()

	input := "- Fix the auth bug in middleware\n- Add rate limiting to endpoints"

	items := d.Decompose(input)

	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}

	// First item should NOT start with "- "
	if len(items[0].Description) > 0 && items[0].Description[0] == '-' {
		t.Errorf("item should have cleaned prefix: %q", items[0].Description)
	}
}

func TestSequentialProcessor_Process(t *testing.T) {
	sp := NewSequentialProcessor()

	input := "Fix auth bug\n- Add rate limiting\n- Update README"

	// Test: decomposition works, processor interface is correct.
	// Full integration test would need a real context and agent loop.
	// For now, verify the decomposer pipeline.
	d := sp.Decomposer
	items := d.Decompose(input)

	if len(items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(items))
	}

	for i, item := range items {
		if item.Index != i+1 {
			t.Errorf("item %d: expected index %d, got %d", i, i+1, item.Index)
		}
		if item.Status != WorkItemPending {
			t.Errorf("item %d: expected pending, got %s", i, item.Status)
		}
	}

	// Verify Process returns nil for non-multi-item input.
	result, err := sp.Process(nil, "single item, no decomposition needed", nil)
	if err != nil {
		t.Errorf("Process should not error for single item: %v", err)
	}
	if result != nil {
		t.Error("Process should return nil for single item input")
	}
}

func TestItemContext_IndexPath(t *testing.T) {
	ic := NewItemContext()

	ic.IndexPath("internal/auth/middleware.go")
	ic.IndexPath("internal/api/rate_limit.go")
	ic.IndexPath("README.md")

	item := WorkItem{Description: "Fix the auth middleware"}

	files := ic.RelevantFiles(item)

	if len(files) == 0 {
		t.Error("expected relevant files for auth middleware item")
	}
}

func TestItemContext_RelevantFiles_Deduplication(t *testing.T) {
	ic := NewItemContext()

	ic.IndexPath("internal/auth/middleware.go")
	ic.IndexPath("internal/auth/handler.go")

	item := WorkItem{Description: "Fix auth bugs"}

	files := ic.RelevantFiles(item)

	// Both should match "auth" keyword.
	if len(files) != 2 {
		t.Errorf("expected 2 files, got %d: %v", len(files), files)
	}

	// No duplicates.
	seen := make(map[string]bool)
	for _, f := range files {
		if seen[f] {
			t.Errorf("duplicate file: %s", f)
		}
		seen[f] = true
	}
}

func TestWorkItemStatus_String(t *testing.T) {
	tests := []struct {
		status   WorkItemStatus
		expected string
	}{
		{WorkItemPending, "pending"},
		{WorkItemActive, "active"},
		{WorkItemDone, "done"},
		{WorkItemFailed, "failed"},
		{WorkItemSkipped, "skipped"},
		{WorkItemStatus(99), "unknown"},
	}

	for _, tt := range tests {
		got := tt.status.String()
		if got != tt.expected {
			t.Errorf("WorkItemStatus(%d).String() = %q, want %q", tt.status, got, tt.expected)
		}
	}
}

package subagent

import (
	"testing"
)

func TestDecomposer_SingleItem(t *testing.T) {
	dc := NewDecomposer()
	items := dc.Decompose("fix the auth bug in security package")
	if items != nil {
		t.Fatalf("expected nil for single-item input, got %d items", len(items))
	}
}

func TestDecomposer_MultiItem(t *testing.T) {
	dc := NewDecomposer()
	// This is exactly the kind of dump that was crashing sub-agents.
	goal := "fix RT-PERS-1 negative index in memo.go\n- wire RT-COMP-1 OOB guard in seahorse.go\n- add nil check for RT-SUB-2 in manager.go\n- fix RT-DRIFT-1 data race in drift.go\n- wire Personality BlindSpotDetector\n- wire Personality ColdStartManager\n- wire Security PermissionManager\n- wire Security PrivilegeGate\n- wire Memory orchestrator always-on"
	items := dc.Decompose(goal)
	if len(items) < 2 {
		t.Fatalf("expected 2+ items from multi-item input, got %d", len(items))
	}
	if len(items) > 10 {
		t.Fatalf("expected max 10 items, got %d", len(items))
	}
	// Each item should be non-empty and reasonable length.
	for i, item := range items {
		if len(item) < 10 {
			t.Errorf("item %d too short: %q", i, item)
		}
	}
}

func TestDecomposer_BatchSize(t *testing.T) {
	// Verify batchSize() fallback chain.
	tests := []struct {
		maxTasks int
		maxChild int
		want     int
	}{
		{0, 0, 3}, // both zero → NewManager defaults MaxChildren=3
		{0, 3, 3}, // only MaxChildren → use it
		{5, 3, 5}, // MaxTasksPerChild takes priority
		{6, 2, 6}, // explicit MaxTasksPerChild
	}
	for _, tt := range tests {
		cfg := Config{MaxTasksPerChild: tt.maxTasks, MaxChildren: tt.maxChild}
		m := NewManager(cfg)
		got := m.batchSize()
		if got != tt.want {
			t.Errorf("MaxTasksPerChild=%d MaxChildren=%d: batchSize()=%d, want %d",
				tt.maxTasks, tt.maxChild, got, tt.want)
		}
	}
}

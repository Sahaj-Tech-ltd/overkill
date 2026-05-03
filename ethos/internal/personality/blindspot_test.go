package personality

import (
	"strings"
	"testing"
)

func TestCheck_ReturnsEmptyBelowThreshold(t *testing.T) {
	bsd := NewBlindSpotDetector()
	bsd.Observe("debug")
	bsd.Observe("debug")
	bsd.Observe("debug")
	msg, ok := bsd.Check()
	if ok {
		t.Fatalf("expected no alert below threshold, got: %s", msg)
	}
	if msg != "" {
		t.Fatalf("expected empty message, got: %s", msg)
	}
}

func TestCheck_ReturnsObservationAtThreshold(t *testing.T) {
	bsd := NewBlindSpotDetector()
	bsd.Observe("fix")
	bsd.Observe("fix")
	bsd.Observe("fix")
	bsd.Observe("fix")
	msg, ok := bsd.Check()
	if !ok {
		t.Fatal("expected alert at threshold")
	}
	if !strings.Contains(msg, "fix") {
		t.Fatalf("expected message to mention 'fix', got: %s", msg)
	}
	if !strings.Contains(msg, "4 times") {
		t.Fatalf("expected message to mention count, got: %s", msg)
	}
}

func TestCheck_Deduplicates(t *testing.T) {
	bsd := NewBlindSpotDetector()
	for i := 0; i < 4; i++ {
		bsd.Observe("refactor")
	}
	_, ok := bsd.Check()
	if !ok {
		t.Fatal("expected first alert")
	}
	_, ok = bsd.Check()
	if ok {
		t.Fatal("expected no duplicate alert for same pattern")
	}
}

func TestCheck_RespectsMaxAlerts(t *testing.T) {
	bsd := NewBlindSpotDetector()
	for i := 0; i < 4; i++ {
		bsd.Observe("debug")
	}
	_, ok := bsd.Check()
	if !ok {
		t.Fatal("expected first alert")
	}
	for i := 0; i < 4; i++ {
		bsd.Observe("fix")
	}
	_, ok = bsd.Check()
	if ok {
		t.Fatal("expected no alert after maxAlerts reached")
	}
}

func TestLoadFromJournal_SeedsPatterns(t *testing.T) {
	bsd := NewBlindSpotDetector()
	entries := []BlindSpotEntry{
		{Type: "user_input", Content: "fix the login bug"},
		{Type: "user_input", Content: "fix the auth error"},
		{Type: "tool_call", Content: "fix the tests"},
		{Type: "tool_call", Content: "fix the build"},
	}
	bsd.LoadFromJournal(entries)
	msg, ok := bsd.Check()
	if !ok {
		t.Fatal("expected alert from journal-seeded patterns")
	}
	if !strings.Contains(msg, "fix") {
		t.Fatalf("expected message about 'fix', got: %s", msg)
	}
}

func TestObserve_IncrementsPattern(t *testing.T) {
	bsd := NewBlindSpotDetector()
	bsd.Observe("debug")
	bsd.Observe("debug")
	if bsd.patterns["debug"] != 2 {
		t.Fatalf("expected count 2, got %d", bsd.patterns["debug"])
	}
	bsd.Observe("debug")
	bsd.Observe("debug")
	if bsd.patterns["debug"] != 4 {
		t.Fatalf("expected count 4, got %d", bsd.patterns["debug"])
	}
}

func TestReset_AllowsNewAlerts(t *testing.T) {
	bsd := NewBlindSpotDetector()
	for i := 0; i < 4; i++ {
		bsd.Observe("update")
	}
	_, ok := bsd.Check()
	if !ok {
		t.Fatal("expected first alert")
	}
	bsd.Reset()
	_, ok = bsd.Check()
	if !ok {
		t.Fatal("expected alert again after reset")
	}
}

func TestCheck_MultiplePatterns(t *testing.T) {
	bsd := NewBlindSpotDetector()
	for i := 0; i < 4; i++ {
		bsd.Observe("debug")
		bsd.Observe("fix")
	}
	_, ok := bsd.Check()
	if !ok {
		t.Fatal("expected first alert")
	}
	_, ok = bsd.Check()
	if ok {
		t.Fatal("expected no second alert due to maxAlerts=1")
	}
}

func TestLoadFromJournal_IgnoresIrrelevantTypes(t *testing.T) {
	bsd := NewBlindSpotDetector()
	entries := []BlindSpotEntry{
		{Type: "system_log", Content: "fix the thing"},
		{Type: "metrics", Content: "debug the issue"},
	}
	bsd.LoadFromJournal(entries)
	if len(bsd.patterns) != 0 {
		t.Fatalf("expected no patterns from irrelevant entry types, got %d", len(bsd.patterns))
	}
}

package compaction

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func seg(id string, content string, age time.Duration, accessCount int, pinned bool) *Segment {
	now := time.Now()
	return &Segment{
		ID:           id,
		Content:      content,
		CreatedAt:    now.Add(-age),
		LastAccessed: now.Add(-age),
		AccessCount:  accessCount,
		Pinned:       pinned,
	}
}

func TestScore_PinnedReturnsInfinity(t *testing.T) {
	s := seg("p", "x", time.Hour, 0, true)
	got := Score(s, ImportanceOptions{})
	if got < 1e15 {
		t.Errorf("pinned should return infinity-ish, got %v", got)
	}
}

func TestScore_RecencyMattersMost(t *testing.T) {
	fresh := seg("a", "short", time.Second, 0, false)
	stale := seg("b", "short", time.Hour, 0, false)
	if Score(fresh, ImportanceOptions{}) <= Score(stale, ImportanceOptions{}) {
		t.Errorf("fresh should outscore stale: fresh=%v stale=%v",
			Score(fresh, ImportanceOptions{}), Score(stale, ImportanceOptions{}))
	}
}

func TestScore_ReuseBumpsScore(t *testing.T) {
	cold := seg("a", "x", time.Hour, 0, false)
	hot := seg("b", "x", time.Hour, 10, false)
	if Score(hot, ImportanceOptions{}) <= Score(cold, ImportanceOptions{}) {
		t.Error("hot (reused) segment should outscore cold")
	}
}

func TestScore_LargeSegmentScoresLower(t *testing.T) {
	small := &Segment{ID: "s", Content: "tiny"}
	big := &Segment{ID: "b", Content: strings.Repeat("x", 5000)}
	if Score(big, ImportanceOptions{}) >= Score(small, ImportanceOptions{}) {
		t.Errorf("larger segment should score lower (more evictable)")
	}
}

func TestRank_OrdersByScore(t *testing.T) {
	segs := []*Segment{
		seg("stale", "x", time.Hour, 0, false),
		seg("fresh", "x", time.Second, 5, false),
		seg("pinned", "x", time.Hour, 0, true),
	}
	ranked := Rank(segs, ImportanceOptions{})
	// Lowest score first: stale → fresh → pinned (infinity).
	if ranked[0].ID != "stale" || ranked[2].ID != "pinned" {
		t.Errorf("unexpected order: %s %s %s", ranked[0].ID, ranked[1].ID, ranked[2].ID)
	}
}

func TestCompact_NoActionUnderBudget(t *testing.T) {
	segs := []*Segment{
		seg("a", "short", time.Hour, 0, false),
		seg("b", "short", time.Hour, 0, false),
	}
	keep, evict := Compact(segs, EvictionTarget{MaxTokens: 1000}, ImportanceOptions{})
	if len(keep) != 2 || len(evict) != 0 {
		t.Errorf("under budget → no eviction, got keep=%d evict=%d", len(keep), len(evict))
	}
}

func TestCompact_EvictsLowestFirst(t *testing.T) {
	// Big segments so we're over budget. Stale should evict first.
	segs := []*Segment{
		seg("stale", strings.Repeat("x", 200), time.Hour, 0, false),
		seg("fresh", strings.Repeat("y", 200), time.Second, 5, false),
		seg("pinned", strings.Repeat("z", 200), time.Hour, 0, true),
	}
	keep, evict := Compact(segs, EvictionTarget{MaxTokens: 100}, ImportanceOptions{})
	// Expect stale evicted; pinned + fresh survive.
	if len(evict) != 1 || evict[0].ID != "stale" {
		t.Errorf("expected stale evicted first, got evict=%+v", evictIDs(evict))
	}
	if len(keep) != 2 {
		t.Errorf("expected fresh + pinned kept, got %+v", evictIDs(keep))
	}
}

func TestCompact_RespectsMinKeep(t *testing.T) {
	// All segments are stale → all eligible for eviction. MinKeep
	// = 2 should prevent eviction below that floor even if budget
	// isn't met.
	segs := []*Segment{
		seg("a", strings.Repeat("x", 400), time.Hour, 0, false),
		seg("b", strings.Repeat("y", 400), time.Hour, 0, false),
		seg("c", strings.Repeat("z", 400), time.Hour, 0, false),
	}
	keep, _ := Compact(segs, EvictionTarget{MaxTokens: 50, MinKeep: 2}, ImportanceOptions{})
	if len(keep) != 2 {
		t.Errorf("MinKeep=2 should leave at least 2 survivors, got %d", len(keep))
	}
}

func TestCompact_NeverEvictsPinned(t *testing.T) {
	segs := []*Segment{
		seg("a", strings.Repeat("x", 1000), time.Hour, 0, true),
		seg("b", strings.Repeat("y", 1000), time.Hour, 0, true),
	}
	keep, evict := Compact(segs, EvictionTarget{MaxTokens: 10}, ImportanceOptions{})
	if len(evict) != 0 {
		t.Errorf("pinned should never evict, got %d", len(evict))
	}
	if len(keep) != 2 {
		t.Errorf("pinned should always survive, got %d", len(keep))
	}
}

func TestCompact_KeepPreservesCallerOrder(t *testing.T) {
	segs := []*Segment{
		seg("first", "short", time.Hour, 5, false),
		seg("second", strings.Repeat("x", 5000), time.Hour, 0, false), // evictable
		seg("third", "short", time.Hour, 5, false),
	}
	keep, _ := Compact(segs, EvictionTarget{MaxTokens: 100}, ImportanceOptions{})
	if len(keep) < 2 {
		t.Fatalf("expected at least 2 survivors, got %d", len(keep))
	}
	if keep[0].ID != "first" || keep[len(keep)-1].ID != "third" {
		t.Errorf("caller-supplied order should be preserved: %+v", evictIDs(keep))
	}
}

func TestHierarchicalCompact_CallsSummaryWriterForEachEvict(t *testing.T) {
	segs := []*Segment{
		seg("stale", strings.Repeat("x", 400), time.Hour, 0, false),
		seg("fresh", strings.Repeat("y", 100), time.Second, 5, false),
	}
	var written []string
	writer := SummaryWriterFunc(func(_ context.Context, s *Segment, summary string) error {
		written = append(written, s.ID)
		return nil
	})
	summarize := func(s *Segment) string { return "summary of " + s.ID }

	keep, evict, err := HierarchicalCompact(
		context.Background(),
		segs,
		EvictionTarget{MaxTokens: 50},
		ImportanceOptions{},
		summarize,
		writer,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(evict) == 0 {
		t.Fatal("expected eviction")
	}
	if len(written) != len(evict) {
		t.Errorf("summary writer should fire once per eviction: %d vs %d", len(written), len(evict))
	}
	if len(keep) == 0 {
		t.Error("at least one survivor expected")
	}
}

func TestHierarchicalCompact_SkipsEmptySummary(t *testing.T) {
	segs := []*Segment{seg("evict", strings.Repeat("x", 400), time.Hour, 0, false)}
	var written []string
	writer := SummaryWriterFunc(func(_ context.Context, s *Segment, _ string) error {
		written = append(written, s.ID)
		return nil
	})
	summarize := func(*Segment) string { return "" }

	_, _, _ = HierarchicalCompact(
		context.Background(),
		segs,
		EvictionTarget{MaxTokens: 1},
		ImportanceOptions{},
		summarize,
		writer,
	)
	if len(written) != 0 {
		t.Errorf("empty summary should not invoke writer, got %d calls", len(written))
	}
}

func TestHierarchicalCompact_WriterErrorRecorded(t *testing.T) {
	segs := []*Segment{
		seg("a", strings.Repeat("x", 400), time.Hour, 0, false),
		seg("b", strings.Repeat("y", 400), time.Hour, 0, false),
	}
	calls := 0
	writer := SummaryWriterFunc(func(_ context.Context, _ *Segment, _ string) error {
		calls++
		return errors.New("disk full")
	})
	summarize := func(s *Segment) string { return "x" }

	_, _, err := HierarchicalCompact(
		context.Background(),
		segs,
		EvictionTarget{MaxTokens: 1},
		ImportanceOptions{},
		summarize,
		writer,
	)
	if err == nil {
		t.Error("writer error should surface")
	}
	if calls < 1 {
		t.Error("writer should be attempted")
	}
}

func TestEnsureSegmentsValid(t *testing.T) {
	if err := EnsureSegmentsValid([]*Segment{{ID: "a"}, {ID: "b"}}); err != nil {
		t.Errorf("valid segments should pass, got %v", err)
	}
	if err := EnsureSegmentsValid([]*Segment{{ID: ""}}); err == nil {
		t.Error("empty ID should fail")
	}
	if err := EnsureSegmentsValid([]*Segment{nil}); err == nil {
		t.Error("nil segment should fail")
	}
}

func TestFormatEvictionReport(t *testing.T) {
	got := FormatEvictionReport(5, 1000, 3, 400)
	if !strings.Contains(got, "compacted 2 segments") {
		t.Errorf("report should mention drop count: %s", got)
	}
	if !strings.Contains(got, "600 saved") {
		t.Errorf("report should mention savings: %s", got)
	}
	got = FormatEvictionReport(5, 1000, 5, 1000)
	if !strings.Contains(got, "no compaction") {
		t.Errorf("no-drop case: %s", got)
	}
}

func evictIDs(segs []*Segment) []string {
	out := make([]string, len(segs))
	for i, s := range segs {
		out[i] = s.ID
	}
	return out
}

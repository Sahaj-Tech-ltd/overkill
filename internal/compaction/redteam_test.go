package compaction

// Red-team tests for internal/compaction — adversarial edge cases.
// Run with: go test -race -count=1 -timeout 30s ./internal/compaction/...

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tokenizer"
)

// maxInt is the largest int value on this platform.
const redteamMaxInt = int(^uint(0) >> 1)

// ──────────────────────────────────────────────────────────────────────────────
// 1. Score — edge cases
// ──────────────────────────────────────────────────────────────────────────────

// TokenCost = 0 and empty Content → estimatedTokens() returns 0.
// Score should not divide by zero; cost component must be finite.
func TestRedTeam_Score_ZeroTokens(t *testing.T) {
	s := &Segment{
		ID:        "zero",
		Content:   "",
		TokenCost: 0,
		CreatedAt: time.Now(),
	}
	got := Score(s, ImportanceOptions{})
	if math.IsNaN(got) || math.IsInf(got, 0) {
		t.Errorf("Score with zero tokens must be finite, got %v", got)
	}
}

// Negative TokenCost: estimatedTokens() guards with `if s.TokenCost > 0` so it
// falls back to content-based estimate — should be safe. Verify no panic / NaN.
func TestRedTeam_Score_NegativeTokenCost(t *testing.T) {
	s := &Segment{
		ID:        "neg",
		Content:   "some text",
		TokenCost: -9999,
		CreatedAt: time.Now(),
	}
	got := Score(s, ImportanceOptions{})
	if math.IsNaN(got) || math.IsInf(got, 0) {
		t.Errorf("Score with negative TokenCost must be finite, got %v", got)
	}
}

// MaxInt tokens — int overflow risk in cost formula `100 / (100 + float64(tokens))`.
func TestRedTeam_Score_MaxIntTokenCost(t *testing.T) {
	s := &Segment{
		ID:        "huge",
		Content:   "x",
		TokenCost: redteamMaxInt,
		CreatedAt: time.Now(),
	}
	got := Score(s, ImportanceOptions{})
	if math.IsNaN(got) || math.IsInf(got, 0) {
		t.Errorf("Score with MaxInt TokenCost must be finite, got %v", got)
	}
	if got < 0 {
		t.Errorf("Score with MaxInt TokenCost must be >= 0, got %v", got)
	}
}

// LastAccessed = zero time AND CreatedAt = zero time → recency stays at 1.0.
func TestRedTeam_Score_ZeroBothTimestamps(t *testing.T) {
	s := &Segment{
		ID:      "nevertouched",
		Content: "hello",
		// Both CreatedAt and LastAccessed are zero value
	}
	opts := ImportanceOptions{Now: func() time.Time { return time.Now() }}
	got := Score(s, opts)
	if math.IsNaN(got) || math.IsInf(got, 0) {
		t.Errorf("Score with zero timestamps must be finite, got %v", got)
	}
}

// UseCount = MaxInt32 — diminishing returns formula `1 - 1/(1+count)` must not
// overflow or become exactly 1.0 (which would be indistinguishable from pinned).
func TestRedTeam_Score_MaxAccessCount(t *testing.T) {
	const maxInt32 = 1<<31 - 1
	s := &Segment{
		ID:          "hotdog",
		Content:     "x",
		AccessCount: maxInt32,
		CreatedAt:   time.Now(),
	}
	got := Score(s, ImportanceOptions{})
	if math.IsNaN(got) || math.IsInf(got, 0) {
		t.Errorf("Score with MaxInt32 AccessCount must be finite, got %v", got)
	}
	if got >= 1e18 {
		t.Errorf("non-pinned segment must score below pinned sentinel (1e18), got %v", got)
	}
}

// Pinned segment must score exactly the sentinel (1e18), not +Inf.
func TestRedTeam_Score_PinnedExactSentinel(t *testing.T) {
	s := &Segment{ID: "pinned", Pinned: true, Content: "anything"}
	got := Score(s, ImportanceOptions{})
	if got != 1e18 {
		t.Errorf("pinned sentinel should be exactly 1e18, got %v", got)
	}
	if math.IsInf(got, 0) {
		t.Errorf("pinned sentinel must not be +Inf (sort comparisons break), got %v", got)
	}
}

// All segments have identical scores — sort should be stable; no panic;
// keep+evict partition must cover all segments.
func TestRedTeam_Score_AllIdenticalScores(t *testing.T) {
	now := time.Now()
	fixedOpts := ImportanceOptions{Now: func() time.Time { return now }}

	segs := make([]*Segment, 5)
	for i := range segs {
		segs[i] = &Segment{
			ID:           fmt.Sprintf("s%d", i),
			Content:      "equal",
			TokenCost:    100,
			CreatedAt:    now.Add(-time.Hour),
			LastAccessed: now.Add(-time.Hour),
		}
	}

	// Verify scores are actually identical.
	ref := Score(segs[0], fixedOpts)
	for _, s := range segs[1:] {
		if Score(s, fixedOpts) != ref {
			t.Skip("scores are not identical; test premise invalid")
		}
	}

	// Total = 5×100 = 500 tokens; budget = 50 → over budget.
	keep, evict := Compact(segs, EvictionTarget{MaxTokens: 50}, fixedOpts)
	if len(keep)+len(evict) != len(segs) {
		t.Errorf("keep+evict (%d+%d) != total (%d)", len(keep), len(evict), len(segs))
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// 2. Compact — adversarial inputs
// ──────────────────────────────────────────────────────────────────────────────

// Empty segment list → must return empty (or nil), not panic.
func TestRedTeam_Compact_EmptySlice(t *testing.T) {
	keep, evict := Compact([]*Segment{}, EvictionTarget{MaxTokens: 100}, ImportanceOptions{})
	if len(keep) != 0 || len(evict) != 0 {
		t.Errorf("empty input → empty output, got keep=%d evict=%d", len(keep), len(evict))
	}
}

// All segments pinned, budget = 0 → early-return fires (MaxTokens <= 0) and
// returns (segments, nil). Nothing should evict.
func TestRedTeam_Compact_AllPinnedBudgetZero(t *testing.T) {
	segs := []*Segment{
		{ID: "p1", Pinned: true, Content: strings.Repeat("x", 100)},
		{ID: "p2", Pinned: true, Content: strings.Repeat("y", 100)},
	}
	keep, evict := Compact(segs, EvictionTarget{MaxTokens: 0}, ImportanceOptions{})
	if len(evict) != 0 {
		t.Errorf("all-pinned with budget=0: expected 0 evictions, got %d", len(evict))
	}
	if len(keep) != len(segs) {
		t.Errorf("all-pinned with budget=0: expected all %d in keep, got %d", len(segs), len(keep))
	}
}

// All segments pinned, budget = 1 (tiny positive) → pinned guard must protect them.
func TestRedTeam_Compact_AllPinnedTinyPositiveBudget(t *testing.T) {
	segs := []*Segment{
		{ID: "p1", Pinned: true, TokenCost: 500},
		{ID: "p2", Pinned: true, TokenCost: 500},
	}
	keep, evict := Compact(segs, EvictionTarget{MaxTokens: 1}, ImportanceOptions{})
	if len(evict) != 0 {
		t.Errorf("pinned segs must not evict even at budget=1, got %d evicted", len(evict))
	}
	if len(keep) != 2 {
		t.Errorf("expected 2 kept, got %d", len(keep))
	}
}

// Target budget larger than total tokens → all segments kept, none evicted.
func TestRedTeam_Compact_BudgetExceedsTotalTokens(t *testing.T) {
	segs := []*Segment{
		{ID: "a", Content: "short", TokenCost: 10},
		{ID: "b", Content: "also short", TokenCost: 10},
	}
	keep, evict := Compact(segs, EvictionTarget{MaxTokens: 99999}, ImportanceOptions{})
	if len(evict) != 0 {
		t.Errorf("budget > total → no eviction, got %d evicted", len(evict))
	}
	if len(keep) != 2 {
		t.Errorf("expected all 2 kept, got %d", len(keep))
	}
}

// MinKeep = 0 → Compact may evict everything non-pinned. Must not panic.
func TestRedTeam_Compact_MinKeepZero(t *testing.T) {
	segs := []*Segment{
		{ID: "a", TokenCost: 400, CreatedAt: time.Now().Add(-time.Hour)},
		{ID: "b", TokenCost: 400, CreatedAt: time.Now().Add(-time.Hour)},
	}
	keep, evict := Compact(segs, EvictionTarget{MaxTokens: 1, MinKeep: 0}, ImportanceOptions{})
	if len(keep)+len(evict) != len(segs) {
		t.Errorf("MinKeep=0: partition sum %d != %d", len(keep)+len(evict), len(segs))
	}
}

// MinKeep > len(segments) → must not panic; no segment should be evicted.
func TestRedTeam_Compact_MinKeepExceedsLen(t *testing.T) {
	segs := []*Segment{
		{ID: "a", TokenCost: 400, CreatedAt: time.Now().Add(-time.Hour)},
		{ID: "b", TokenCost: 400, CreatedAt: time.Now().Add(-time.Hour)},
	}
	keep, evict := Compact(segs, EvictionTarget{MaxTokens: 1, MinKeep: 100}, ImportanceOptions{})
	if len(evict) != 0 {
		t.Errorf("MinKeep > len(segs) → nothing should evict, got %d evicted", len(evict))
	}
	if len(keep) != 2 {
		t.Errorf("expected 2 in keep, got %d", len(keep))
	}
}

// Single segment that exceeds budget, MinKeep=0 — may be evicted.
func TestRedTeam_Compact_SingleSegmentOverBudget_MinKeep0(t *testing.T) {
	segs := []*Segment{
		{ID: "big", TokenCost: 5000, CreatedAt: time.Now().Add(-time.Hour)},
	}
	keep, evict := Compact(segs, EvictionTarget{MaxTokens: 1, MinKeep: 0}, ImportanceOptions{})
	if len(keep)+len(evict) != 1 {
		t.Errorf("partition sum must be 1, got keep=%d evict=%d", len(keep), len(evict))
	}
}

// Single segment that exceeds budget, MinKeep=1 — must be kept.
func TestRedTeam_Compact_SingleSegmentOverBudget_MinKeep1(t *testing.T) {
	segs := []*Segment{
		{ID: "big", TokenCost: 5000, CreatedAt: time.Now().Add(-time.Hour)},
	}
	keep, evict := Compact(segs, EvictionTarget{MaxTokens: 1, MinKeep: 1}, ImportanceOptions{})
	if len(evict) != 0 {
		t.Errorf("MinKeep=1 with single segment must not evict it, got evict=%d", len(evict))
	}
	if len(keep) != 1 {
		t.Errorf("single seg must stay in keep, got %d", len(keep))
	}
}

// Negative MinKeep should be clamped to 0 and not panic.
func TestRedTeam_Compact_NegativeMinKeep(t *testing.T) {
	segs := []*Segment{
		{ID: "a", TokenCost: 400, CreatedAt: time.Now().Add(-time.Hour)},
	}
	keep, evict := Compact(segs, EvictionTarget{MaxTokens: 1, MinKeep: -5}, ImportanceOptions{})
	if len(keep)+len(evict) != 1 {
		t.Errorf("negative MinKeep: partition sum must be 1, got %d+%d", len(keep), len(evict))
	}
}

// Nil segment in slice — document behavior (panic or safe handling).
func TestRedTeam_Compact_NilSegmentInSlice(t *testing.T) {
	panicked := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				panicked = true
				t.Logf("Compact panicked on nil segment: %v (document this behavior)", r)
			}
		}()
		segs := []*Segment{
			{ID: "a", TokenCost: 5},
			nil,
			{ID: "b", TokenCost: 5},
		}
		keep, evict := Compact(segs, EvictionTarget{MaxTokens: 1000}, ImportanceOptions{})
		t.Logf("nil segment in slice: keep=%d evict=%d (no panic)", len(keep), len(evict))
	}()
	if panicked {
		t.Log("FINDING: Compact panics on nil segment — caller must guard with EnsureSegmentsValid")
	}
}

// Duplicate segment IDs — evictSet is keyed by ID; second occurrence of the
// same ID may be incorrectly excluded from both lists. Verify partition coverage.
func TestRedTeam_Compact_DuplicateSegmentIDs(t *testing.T) {
	segs := []*Segment{
		{ID: "dup", TokenCost: 400, CreatedAt: time.Now().Add(-time.Hour)},
		{ID: "dup", TokenCost: 400, CreatedAt: time.Now().Add(-time.Hour)},
		{ID: "unique", TokenCost: 5},
	}
	keep, evict := Compact(segs, EvictionTarget{MaxTokens: 50, MinKeep: 0}, ImportanceOptions{})
	total := len(keep) + len(evict)
	if total != len(segs) {
		t.Errorf("duplicate IDs: keep+evict (%d+%d=%d) != total (%d) — possible segment loss",
			len(keep), len(evict), total, len(segs))
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// 3. HierarchicalCompact — adversarial SummaryWriter cases
// ──────────────────────────────────────────────────────────────────────────────

// Writer always errors — compaction must complete; err must be non-nil.
func TestRedTeam_HierarchicalCompact_WriterAlwaysErrors(t *testing.T) {
	segs := []*Segment{
		{ID: "evictMe", TokenCost: 500, CreatedAt: time.Now().Add(-time.Hour)},
		{ID: "keepMe", TokenCost: 2, AccessCount: 10, CreatedAt: time.Now()},
	}
	errWriter := SummaryWriterFunc(func(_ context.Context, _ *Segment, _ string) error {
		return fmt.Errorf("disk full")
	})
	summarize := func(s *Segment) string { return "summary: " + s.ID }

	keep, evict, err := HierarchicalCompact(
		context.Background(),
		segs,
		EvictionTarget{MaxTokens: 10},
		ImportanceOptions{},
		summarize,
		errWriter,
	)
	if len(keep)+len(evict) != len(segs) {
		t.Errorf("partition sum wrong: keep=%d evict=%d total=%d", len(keep), len(evict), len(segs))
	}
	if err == nil {
		t.Error("expected non-nil error when writer always fails")
	}
}

// Writer that panics — documents whether the panic propagates.
func TestRedTeam_HierarchicalCompact_WriterPanics(t *testing.T) {
	segs := []*Segment{
		{ID: "evict", TokenCost: 500, CreatedAt: time.Now().Add(-time.Hour)},
	}
	panicWriter := SummaryWriterFunc(func(_ context.Context, _ *Segment, _ string) error {
		panic("writer exploded")
	})
	summarize := func(s *Segment) string { return "summary" }

	panicked := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				panicked = true
				t.Logf("HierarchicalCompact propagated writer panic: %v (document this behavior)", r)
			}
		}()
		_, _, _ = HierarchicalCompact(
			context.Background(),
			segs,
			EvictionTarget{MaxTokens: 1},
			ImportanceOptions{},
			summarize,
			panicWriter,
		)
	}()
	if panicked {
		t.Log("FINDING: writer panic propagates — callers must recover or writer must not panic")
	}
}

// Nil SummaryWriter — must not panic; return keep/evict without writing.
func TestRedTeam_HierarchicalCompact_NilWriter(t *testing.T) {
	segs := []*Segment{
		{ID: "a", TokenCost: 400, CreatedAt: time.Now().Add(-time.Hour)},
	}
	summarize := func(s *Segment) string { return "summary" }

	keep, evict, err := HierarchicalCompact(
		context.Background(),
		segs,
		EvictionTarget{MaxTokens: 1},
		ImportanceOptions{},
		summarize,
		nil,
	)
	if err != nil {
		t.Errorf("nil writer must not error, got %v", err)
	}
	if len(keep)+len(evict) != len(segs) {
		t.Errorf("partition sum wrong: keep=%d evict=%d", len(keep), len(evict))
	}
}

// Nil summarize function — must not panic; writer must not be called.
func TestRedTeam_HierarchicalCompact_NilSummarize(t *testing.T) {
	segs := []*Segment{
		{ID: "a", TokenCost: 400, CreatedAt: time.Now().Add(-time.Hour)},
	}
	var calls int
	writer := SummaryWriterFunc(func(_ context.Context, _ *Segment, _ string) error {
		calls++
		return nil
	})

	keep, evict, err := HierarchicalCompact(
		context.Background(),
		segs,
		EvictionTarget{MaxTokens: 1},
		ImportanceOptions{},
		nil, // nil summarize
		writer,
	)
	if err != nil {
		t.Errorf("nil summarize must not error, got %v", err)
	}
	if calls != 0 {
		t.Errorf("nil summarize must not invoke writer, got %d calls", calls)
	}
	if len(keep)+len(evict) != len(segs) {
		t.Errorf("partition sum wrong: keep=%d evict=%d", len(keep), len(evict))
	}
}

// Empty input — must not panic; all outputs empty.
func TestRedTeam_HierarchicalCompact_EmptyInput(t *testing.T) {
	var calls int
	writer := SummaryWriterFunc(func(_ context.Context, _ *Segment, _ string) error {
		calls++
		return nil
	})
	summarize := func(s *Segment) string { return "summary" }

	keep, evict, err := HierarchicalCompact(
		context.Background(),
		[]*Segment{},
		EvictionTarget{MaxTokens: 100},
		ImportanceOptions{},
		summarize,
		writer,
	)
	if err != nil {
		t.Errorf("empty input must not error, got %v", err)
	}
	if calls != 0 {
		t.Errorf("empty input must not call writer, got %d calls", calls)
	}
	if len(keep) != 0 || len(evict) != 0 {
		t.Errorf("empty in → empty out, got keep=%d evict=%d", len(keep), len(evict))
	}
}

// Cancelled context — Compact itself doesn't consult ctx; writer may.
// Ensure no panic regardless.
func TestRedTeam_HierarchicalCompact_CancelledContext(t *testing.T) {
	segs := []*Segment{
		{ID: "a", TokenCost: 400, CreatedAt: time.Now().Add(-time.Hour)},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	writer := SummaryWriterFunc(func(c context.Context, _ *Segment, _ string) error {
		if c.Err() != nil {
			return c.Err()
		}
		return nil
	})
	summarize := func(s *Segment) string { return "summary" }

	keep, evict, _ := HierarchicalCompact(ctx, segs, EvictionTarget{MaxTokens: 1}, ImportanceOptions{}, summarize, writer)
	if len(keep)+len(evict) != len(segs) {
		t.Errorf("cancelled ctx: partition sum %d != %d", len(keep)+len(evict), len(segs))
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// 4. Concurrency — Compact called from 50 goroutines simultaneously
// ──────────────────────────────────────────────────────────────────────────────

func TestRedTeam_Compact_ConcurrentGoroutines(t *testing.T) {
	now := time.Now()
	segs := make([]*Segment, 20)
	for i := range segs {
		segs[i] = &Segment{
			ID:           fmt.Sprintf("seg%d", i),
			TokenCost:    100 + i*10,
			CreatedAt:    now.Add(-time.Duration(i) * time.Minute),
			LastAccessed: now.Add(-time.Duration(i) * time.Minute),
			AccessCount:  i,
		}
	}

	const goroutines = 50
	var wg sync.WaitGroup
	errs := make(chan string, goroutines)

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					errs <- fmt.Sprintf("goroutine %d panicked: %v", id, r)
				}
			}()
			budget := 200 + rand.Intn(800) //nolint:gosec
			keep, evict := Compact(segs, EvictionTarget{MaxTokens: budget, MinKeep: 2}, ImportanceOptions{})
			total := len(keep) + len(evict)
			if total != len(segs) {
				errs <- fmt.Sprintf("goroutine %d: partition sum %d != %d (budget=%d)",
					id, total, len(segs), budget)
			}
		}(g)
	}

	wg.Wait()
	close(errs)
	for msg := range errs {
		t.Error(msg)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// 5. LCMCompactor — threshold boundary tests
// ──────────────────────────────────────────────────────────────────────────────

// ShouldCompact at exactly the soft threshold.
func TestRedTeam_LCMCompactor_ExactlySoftThreshold(t *testing.T) {
	est := tokenizer.NewEstimator()
	mp := &mockProvider{}
	c := NewLCMCompactor(mp, est)

	opts := CompactOptions{
		MaxTokens:     1000,
		SoftThreshold: 0.5,
		HardThreshold: 0.95,
	}
	// ~500 tokens of content (4 chars/token × 2000 chars ≈ 500 tokens)
	content := strings.Repeat("a", 2000)
	msgs := []providers.Message{{Role: "user", Content: content}}
	actual := c.EstimateUsage(msgs, "")
	if actual == 0 {
		t.Skip("tokenizer returned 0 — cannot test boundary")
	}
	// Set MaxTokens so usage / MaxTokens == SoftThreshold exactly.
	opts.MaxTokens = int(float64(actual) / opts.SoftThreshold)

	should, pct := c.ShouldCompact(msgs, opts)
	t.Logf("at soft threshold: should=%v pct=%.6f soft=%.6f", should, pct, opts.SoftThreshold)
	if !should {
		t.Errorf("at exactly soft threshold, ShouldCompact must return true, got pct=%.6f", pct)
	}
}

// ShouldCompact at exactly the hard threshold.
func TestRedTeam_LCMCompactor_ExactlyHardThreshold(t *testing.T) {
	est := tokenizer.NewEstimator()
	mp := &mockProvider{}
	c := NewLCMCompactor(mp, est)

	opts := CompactOptions{
		MaxTokens:     1000,
		SoftThreshold: 0.5,
		HardThreshold: 0.95,
	}
	content := strings.Repeat("b", 3800) // ~950 tokens
	msgs := []providers.Message{{Role: "user", Content: content}}
	actual := c.EstimateUsage(msgs, "")
	if actual == 0 {
		t.Skip("tokenizer returned 0")
	}
	opts.MaxTokens = int(float64(actual) / opts.HardThreshold)

	should, pct := c.ShouldCompact(msgs, opts)
	t.Logf("at hard threshold: should=%v pct=%.6f hard=%.6f", should, pct, opts.HardThreshold)
	if !should {
		t.Errorf("at exactly hard threshold, ShouldCompact must return true, got pct=%.6f", pct)
	}
}

// ShouldCompact well above hard threshold.
func TestRedTeam_LCMCompactor_AboveHardThreshold(t *testing.T) {
	est := tokenizer.NewEstimator()
	mp := &mockProvider{}
	c := NewLCMCompactor(mp, est)

	opts := CompactOptions{
		MaxTokens:     10,
		SoftThreshold: 0.5,
		HardThreshold: 0.95,
	}
	msgs := []providers.Message{{Role: "user", Content: strings.Repeat("z", 5000)}}
	should, pct := c.ShouldCompact(msgs, opts)
	if !should {
		t.Errorf("way above hard threshold: must compact, got pct=%.4f", pct)
	}
	if pct < opts.HardThreshold {
		t.Errorf("pct %.4f must be >= hard threshold %.4f", pct, opts.HardThreshold)
	}
}

// ShouldCompact with MaxTokens = 0 — must return false, no divide-by-zero NaN.
func TestRedTeam_LCMCompactor_ShouldCompact_ZeroMaxTokens(t *testing.T) {
	est := tokenizer.NewEstimator()
	mp := &mockProvider{}
	c := NewLCMCompactor(mp, est)

	msgs := []providers.Message{{Role: "user", Content: "hello"}}
	should, pct := c.ShouldCompact(msgs, CompactOptions{MaxTokens: 0})
	if should {
		t.Error("MaxTokens=0 → should not compact (divide-by-zero guard)")
	}
	if math.IsNaN(pct) || math.IsInf(pct, 0) {
		t.Errorf("pct must be finite with MaxTokens=0, got %v", pct)
	}
}

// Compact with negative MaxTokens — should behave like budget=0 (no-op or safe).
func TestRedTeam_LCMCompactor_NegativeMaxTokens(t *testing.T) {
	est := tokenizer.NewEstimator()
	mp := &mockProvider{}
	c := NewLCMCompactor(mp, est)

	msgs := makeMessages(30, "content to compact")
	opts := defaultOpts()
	opts.MaxTokens = -1

	// Should not panic; result may be nil (treated as no compaction needed).
	result, err := c.Compact(context.Background(), msgs, opts)
	if err != nil {
		t.Logf("negative MaxTokens returned error (acceptable): %v", err)
	}
	_ = result // nil is fine
}

// ──────────────────────────────────────────────────────────────────────────────
// 6. halfLifeDecay — boundary arithmetic
// ──────────────────────────────────────────────────────────────────────────────

func TestRedTeam_HalfLifeDecay_ZeroHalfLife(t *testing.T) {
	got := halfLifeDecay(time.Hour, 0)
	if got != 1.0 {
		t.Errorf("zero halfLife → decay=1.0, got %v", got)
	}
}

func TestRedTeam_HalfLifeDecay_ZeroAge(t *testing.T) {
	got := halfLifeDecay(0, time.Hour)
	if got != 1.0 {
		t.Errorf("zero age → decay=1.0, got %v", got)
	}
}

func TestRedTeam_HalfLifeDecay_VeryLargeAge(t *testing.T) {
	// Age >> 50 × halfLife — loop guard `x < -50` → return 0.
	got := halfLifeDecay(1000*time.Hour, time.Minute)
	if got < 0 || math.IsNaN(got) || math.IsInf(got, 0) {
		t.Errorf("very large age → should clamp to 0, got %v", got)
	}
}

func TestRedTeam_HalfLifeDecay_NegativeAge(t *testing.T) {
	// Negative duration (future timestamp) — guard `if age < 0 { age = 0 }` in Score.
	// halfLifeDecay itself receives raw duration; caller clamps.
	got := halfLifeDecay(-time.Hour, time.Minute)
	if got != 1.0 {
		t.Errorf("negative age → decay=1.0 (guard branch), got %v", got)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// 7. FormatEvictionReport — edge arithmetic
// ──────────────────────────────────────────────────────────────────────────────

// finalTokens > originalTokens → saved would be negative without a guard.
func TestRedTeam_FormatEvictionReport_NegativeSaved(t *testing.T) {
	got := FormatEvictionReport(5, 100, 4, 200)
	if strings.Contains(got, "-") {
		t.Errorf("negative saved must be clamped to 0, got: %s", got)
	}
}

func TestRedTeam_FormatEvictionReport_ZeroInputs(t *testing.T) {
	got := FormatEvictionReport(0, 0, 0, 0)
	if got == "" {
		t.Error("report must not be empty string even with zeros")
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// 8. itoaInt — internal formatter extremes
// ──────────────────────────────────────────────────────────────────────────────

func TestRedTeam_ItoaInt_Extremes(t *testing.T) {
	cases := []int{0, 1, -1, redteamMaxInt, -redteamMaxInt}
	for _, n := range cases {
		got := itoaInt(n)
		if got == "" {
			t.Errorf("itoaInt(%d) returned empty string", n)
		}
	}
}

// Package compaction — importance scoring + hierarchical
// compaction (§8.1 Phase 5 #8, application-layer approximation of
// Cartridge / Neural GC / Fast KV Compaction).
//
// Honest scope: the original papers (Eyuboglu 2025, Li 2026,
// Zweiger 2026) operate on the model's KV cache. We don't have
// model weights or kernel access — we cannot literally implement
// them. What we CAN build is the same shape at the application
// layer:
//
//   - Importance scoring: rank context segments by recency × reuse
//     × pinned-status × token-cost. Cheap segments and frequently-
//     re-read segments stay; expensive-and-untouched ones evict.
//   - Hierarchical compaction: when a segment is evicted, write a
//     short summary into a retrievable index instead of dropping
//     it entirely. The agent can re-fetch via journal_search if
//     it needs the original.
//   - Prefetch on relevance signal: when the agent reads a segment,
//     pull adjacent ones into the cache proactively (composes with
//     internal/speculative — that package handles the read cache;
//     this one handles the eviction policy).
//
// What's HERE: the scoring + eviction logic. The compactor itself
// (lcm.go, compactor.go) already lives in this package; importance.
// go plugs into it as an alternative ranking strategy. The
// hierarchical summary write-path is a SummaryWriter interface so
// the agent can plug in whatever LLM-driven summarizer it wants.
package compaction

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Segment is one chunk of context with the metadata importance
// scoring needs.
type Segment struct {
	// ID is a stable identifier used to track segments across
	// scoring passes. Could be a message ID, tool-call ID, or any
	// opaque caller-supplied tag.
	ID string
	// Content is the actual context bytes. Token-cost estimate
	// derived from len(Content) when TokenCost is 0.
	Content string
	// TokenCost is the caller's estimate (or measured count) for
	// this segment. 0 → estimate as len(Content) / 4 (rough English
	// rule of thumb).
	TokenCost int
	// CreatedAt is when the segment first entered the context.
	CreatedAt time.Time
	// LastAccessed is bumped whenever the agent re-reads the
	// segment. Recency component of the score.
	LastAccessed time.Time
	// AccessCount tracks how often the segment has been re-read.
	// Reuse component of the score.
	AccessCount int
	// Pinned, when true, never evicts. Use for the user's current
	// turn, the active plan, or anything the caller wants to keep
	// regardless of score.
	Pinned bool
	// Kind is operator-readable: "user_input", "agent_reply",
	// "tool_call", "tool_result", "system". Same vocabulary as the
	// flight recorder but here it's just metadata.
	Kind string
}

// estimatedTokens returns TokenCost when set, otherwise an
// approximation. The exact value doesn't matter for ranking — we
// only need a consistent ordering signal.
func (s *Segment) estimatedTokens() int {
	if s.TokenCost > 0 {
		return s.TokenCost
	}
	if s.Content == "" {
		return 0
	}
	// ~4 chars per English token; ceil so any non-empty content
	// scores at least 1.
	t := (len(s.Content) + 3) / 4
	if t < 1 {
		return 1
	}
	return t
}

// ImportanceOptions tunes the score weights.
type ImportanceOptions struct {
	// RecencyHalfLife after which the recency contribution drops
	// to 0.5. Default 30 minutes — context compaction operates on
	// much shorter horizons than memory segments.
	RecencyHalfLife time.Duration
	// RecencyWeight, ReuseWeight, CostWeight scale each axis.
	// Pinned is binary — pinned segments score Infinity, never
	// evict.
	RecencyWeight float64
	ReuseWeight   float64
	// CostWeight is INVERTED: bigger segments get LOWER scores
	// (more attractive to evict). Default 0.5.
	CostWeight float64
	// Now lets tests inject a deterministic clock.
	Now func() time.Time
}

func (o *ImportanceOptions) halfLife() time.Duration {
	if o.RecencyHalfLife > 0 {
		return o.RecencyHalfLife
	}
	return 30 * time.Minute
}

func (o *ImportanceOptions) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now().UTC()
}

func (o *ImportanceOptions) weights() (recency, reuse, cost float64) {
	recency = o.RecencyWeight
	if recency <= 0 || recency != recency || recency > 1e9 {
		recency = 1.0
	}
	reuse = o.ReuseWeight
	if reuse <= 0 || reuse != reuse || reuse > 1e9 {
		reuse = 0.7
	}
	cost = o.CostWeight
	if cost <= 0 || cost != cost || cost > 1e9 {
		cost = 0.5
	}
	return
}

// Score returns the importance score for one segment. Higher =
// keep. Pinned segments return +Inf so the caller's eviction loop
// trivially skips them.
func Score(seg *Segment, opts ImportanceOptions) float64 {
	if seg == nil {
		return 0
	}
	if seg.Pinned {
		return 1e18 // effectively infinity
	}
	rw, ru, cw := opts.weights()
	hl := opts.halfLife()
	now := opts.now()

	// Recency: half-life decay from LastAccessed (or CreatedAt
	// when no read history).
	ts := seg.LastAccessed
	if ts.IsZero() {
		ts = seg.CreatedAt
	}
	recency := 1.0
	if !ts.IsZero() {
		age := now.Sub(ts)
		if age < 0 {
			age = 0
		}
		recency = halfLifeDecay(age, hl)
	}

	// Reuse: diminishing returns on access count. 1 access ≈ 0.5,
	// 10 ≈ 0.91.
	reuse := 0.0
	if seg.AccessCount > 0 {
		reuse = 1.0 - 1.0/(1.0+float64(seg.AccessCount))
	}

	// Cost: inverse-scaled by token count. We want LARGE segments
	// to score LOWER so they evict first when comparable on the
	// other axes.
	tokens := seg.estimatedTokens()
	cost := 1.0
	if tokens > 0 {
		// 100 tokens → 1.0, 500 → ~0.5, 2000 → ~0.2
		cost = 100.0 / (100.0 + float64(tokens))
	}

	return recency*rw + reuse*ru + cost*cw
}

// Rank returns segments sorted by Score, lowest-first — i.e. the
// eviction order. Pinned segments land at the end (highest score).
// Stable sort preserves caller-supplied order for ties.
func Rank(segments []*Segment, opts ImportanceOptions) []*Segment {
	out := make([]*Segment, 0, len(segments))
	scores := make([]float64, 0, len(segments))
	for _, s := range segments {
		if s == nil {
			continue
		}
		out = append(out, s)
		scores = append(scores, Score(s, opts))
	}
	sort.SliceStable(out, func(i, j int) bool {
		return scores[i] < scores[j]
	})
	return out
}

// EvictionTarget describes how aggressively to compact.
type EvictionTarget struct {
	// MaxTokens caps the total token budget after eviction. Caller
	// usually sets this to the model's context window minus a
	// safety margin.
	MaxTokens int
	// MinKeep guarantees at least this many segments survive even
	// if they exceed MaxTokens. Protects the agent from over-
	// compacting itself into amnesia.
	MinKeep int
}

// Compact decides which segments to evict to bring the total
// token cost under target.MaxTokens. Returns (keep, evict). Eviction
// order is lowest-score-first; pinned segments never appear in
// `evict`. Falls back to keeping at least MinKeep segments.
//
// Caller's responsibility: actually drop the evicted segments from
// the live context. We just decide.
func Compact(segments []*Segment, target EvictionTarget, opts ImportanceOptions) (keep []*Segment, evict []*Segment) {
	if target.MaxTokens <= 0 || len(segments) == 0 {
		return segments, nil
	}
	totalTokens := 0
	for _, s := range segments {
		if s != nil {
			totalTokens += s.estimatedTokens()
		}
	}
	if totalTokens <= target.MaxTokens {
		return segments, nil
	}

	// Sort by importance ascending (lowest score = best eviction
	// candidate first).
	ranked := Rank(segments, opts)
	minKeep := target.MinKeep
	if minKeep < 0 {
		minKeep = 0
	}

	// Walk lowest-first, evicting until budget is met or we'd drop
	// below MinKeep.
	evictSet := map[string]bool{}
	for _, s := range ranked {
		if s.Pinned {
			continue
		}
		// Stop if we'd violate MinKeep.
		remaining := len(segments) - len(evictSet)
		if remaining <= minKeep {
			break
		}
		evictSet[s.ID] = true
		totalTokens -= s.estimatedTokens()
		if totalTokens <= target.MaxTokens {
			break
		}
	}

	// Walk caller-supplied order to preserve it in the keep list.
	for _, s := range segments {
		if evictSet[s.ID] {
			evict = append(evict, s)
			continue
		}
		keep = append(keep, s)
	}
	return keep, evict
}

// SummaryWriter is the hook for hierarchical compaction. When the
// caller evicts a segment, it can ask a SummaryWriter to write a
// retrievable summary into a long-term index (typically the
// observation store). The agent can later journal_search to pull
// the summary back when relevance signal demands it.
type SummaryWriter interface {
	WriteSummary(ctx context.Context, segment *Segment, summary string) error
}

// SummaryWriterFunc adapts a function to the interface.
type SummaryWriterFunc func(ctx context.Context, segment *Segment, summary string) error

func (f SummaryWriterFunc) WriteSummary(ctx context.Context, seg *Segment, summary string) error {
	return f(ctx, seg, summary)
}

// HierarchicalCompact extends Compact with summary-write-on-evict.
// For each evicted segment, the supplied Summarizer produces a
// short prose summary, and the SummaryWriter persists it. Errors
// in the write path are swallowed (best-effort) so a failing
// downstream doesn't block compaction.
//
// summarize may return ("", nil) to skip the summary for a
// particular segment (e.g. trivial tool results).
func HierarchicalCompact(
	ctx context.Context,
	segments []*Segment,
	target EvictionTarget,
	opts ImportanceOptions,
	summarize func(*Segment) string,
	writer SummaryWriter,
) (keep []*Segment, evict []*Segment, err error) {
	keep, evict = Compact(segments, target, opts)
	if writer == nil || summarize == nil {
		return keep, evict, nil
	}
	for _, s := range evict {
		summary := summarize(s)
		if strings.TrimSpace(summary) == "" {
			continue
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					if err == nil {
						err = fmt.Errorf("compaction: writer panic: %v", r)
					}
				}
			}()
			if werr := writer.WriteSummary(ctx, s, summary); werr != nil {
				// Best-effort — record first error but keep going.
				if err == nil {
					err = werr
				}
			}
		}()
	}
	return keep, evict, err
}

// halfLifeDecay returns 2^(-age/halfLife). Bounded [0, 1].
func halfLifeDecay(age, halfLife time.Duration) float64 {
	if halfLife <= 0 || age <= 0 {
		return 1.0
	}
	x := -float64(age) / float64(halfLife)
	if x < -50 {
		return 0
	}
	result := 1.0
	for x < 0 {
		if x <= -1 {
			result *= 0.5
			x += 1
		} else {
			result *= 1.0 + x*0.5
			break
		}
	}
	if result < 0 {
		result = 0
	}
	return result
}

// FormatEvictionReport renders a one-line summary of an eviction
// pass: "compacted N segments (M → K tokens)". Useful for the
// daemon log + the user-facing toast.
func FormatEvictionReport(originalCount, originalTokens, finalCount, finalTokens int) string {
	if originalCount == finalCount {
		return "no compaction needed"
	}
	return formatReport(originalCount, originalTokens, finalCount, finalTokens)
}

func formatReport(oc, ot, fc, ft int) string {
	dropped := oc - fc
	if dropped <= 0 {
		return "no compaction needed"
	}
	saved := ot - ft
	if saved < 0 {
		saved = 0
	}
	return sprint("compacted ", dropped, " segments (", ot, " → ", ft, " tokens; ", saved, " saved)")
}

// sprint is a tiny no-dep formatter so the report doesn't need the
// fmt package (keeps the package import surface minimal).
func sprint(parts ...any) string {
	var b strings.Builder
	for _, p := range parts {
		switch v := p.(type) {
		case string:
			b.WriteString(v)
		case int:
			b.WriteString(itoaInt(v))
		}
	}
	return b.String()
}

func itoaInt(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// EnsureSegmentsValid is a sanity check for the caller — returns
// the first error if any segment has an empty ID, or nil when all
// look good. Useful in tests + before passing to Compact.
func EnsureSegmentsValid(segs []*Segment) error {
	for _, s := range segs {
		if s == nil {
			return errors.New("compaction: nil segment in batch")
		}
		if s.ID == "" {
			return errors.New("compaction: segment missing ID")
		}
	}
	return nil
}

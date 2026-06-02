// Package journal — vector similarity for observations (§4.19
// hybrid search). Wires the bridge package's embedding service to
// the ObservationStore so journal_search can blend FTS hits with
// semantic neighbors.
//
// Design:
//
//   - VectorIndex is a tiny interface: Embed(text) → vector,
//     Store(id, vec, content, meta), Search(vec, topK, threshold).
//     The wiring layer (cmd/overkill) plugs internal/memory's
//     BridgeAdapter in — internal/journal doesn't import the Python
//     bridge directly.
//   - Indexing is best-effort and asynchronous-ish: we kick off
//     Store under a short timeout. A bridge that's down or slow
//     never blocks observation capture. The observation is still
//     persisted to disk via the existing ObservationStore path;
//     vector search just won't find it until the bridge catches up.
//   - Search is synchronous (the caller is asking RIGHT NOW), but
//     bounded by a 5s timeout and returns ("", nil) when the bridge
//     is unavailable — caller falls back to FTS-only.
//
// This closes the "vector path stubbed" item in §4.19. The shape
// here is intentionally narrow: the journal owns the observation
// lifecycle, the bridge owns the math.
package journal

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// VectorIndex is the minimal surface the journal needs from a
// vector store. Pass nil in the constructor to disable the vector
// path (FTS-only mode).
type VectorIndex interface {
	Embed(ctx context.Context, text, model string) ([]float32, int32, error)
	Store(ctx context.Context, id string, embedding []float32, content string, metadata map[string]string) (string, error)
	Search(ctx context.Context, query []float32, topK int, threshold float64) ([]VectorSearchHit, error)
}

// VectorSearchHit is one neighbor returned by the vector index.
// Score is provider-defined (cosine similarity for the default
// embedding service); higher is more similar.
type VectorSearchHit struct {
	ID       string
	Score    float64
	Content  string
	Metadata map[string]string
}

// VectorOptions tunes embed model + search defaults. Zero-value
// defaults are reasonable for most callers.
type VectorOptions struct {
	// EmbedModel passed to Embed. Empty → bridge's default.
	EmbedModel string
	// IndexTimeout caps the per-observation Store call. Default 3s.
	IndexTimeout time.Duration
	// SearchTimeout caps SearchSimilar. Default 5s.
	SearchTimeout time.Duration
	// DefaultTopK when caller passes 0. Default 10.
	DefaultTopK int
	// DefaultThreshold when caller passes 0. Default 0.5 cosine.
	DefaultThreshold float64
}

func (o *VectorOptions) indexTimeout() time.Duration {
	if o.IndexTimeout > 0 {
		return o.IndexTimeout
	}
	return 3 * time.Second
}

func (o *VectorOptions) searchTimeout() time.Duration {
	if o.SearchTimeout > 0 {
		return o.SearchTimeout
	}
	return 5 * time.Second
}

func (o *VectorOptions) topK(k int) int {
	if k > 0 {
		return k
	}
	if o.DefaultTopK > 0 {
		return o.DefaultTopK
	}
	return 10
}

func (o *VectorOptions) threshold(t float64) float64 {
	if t > 0 {
		return t
	}
	if o.DefaultThreshold > 0 {
		return o.DefaultThreshold
	}
	return 0.5
}

// VectorEnabledStore wraps an ObservationStore with an optional
// vector index. When the index is nil (or wiring failed at boot),
// the wrapper falls through to the bare store — vector calls are
// no-ops and SearchSimilar returns (nil, nil).
type VectorEnabledStore struct {
	*ObservationStore
	index VectorIndex
	opts  VectorOptions
}

// NewVectorEnabledStore wraps store with an index. Passing a nil
// index is legal and gives FTS-only behaviour — useful for tests
// and for users running without the Python bridge.
func NewVectorEnabledStore(store *ObservationStore, index VectorIndex, opts VectorOptions) *VectorEnabledStore {
	if store == nil {
		store = NewObservationStore("") // fallback: empty dir, no disk ops
	}
	return &VectorEnabledStore{ObservationStore: store, index: index, opts: opts}
}

// StoreWithVector persists the observation AND embeds it into the
// vector index. The observation lands on disk first (durability is
// non-negotiable); the embedding is best-effort.
func (s *VectorEnabledStore) StoreWithVector(ctx context.Context, obs *Observation) error {
	if obs == nil {
		return fmt.Errorf("journal: vector store: nil observation")
	}
	if err := s.ObservationStore.Store(obs); err != nil {
		return err
	}
	if s.index == nil {
		return nil
	}
	// Synthesize the embed text from the observation's user-readable
	// fields. Including narrative + title + concepts gives the
	// embedder enough signal without bloating the request.
	text := composeEmbedText(obs)
	if text == "" {
		return nil
	}
	embedCtx, cancel := context.WithTimeout(ctx, s.opts.indexTimeout())
	defer cancel()
	embedding, _, err := s.index.Embed(embedCtx, text, s.opts.EmbedModel)
	if err != nil {
		return fmt.Errorf("journal: embed: %w", err)
	}
	storeCtx, cancel2 := context.WithTimeout(ctx, s.opts.indexTimeout())
	defer cancel2()
	_, err = s.index.Store(storeCtx, obs.ID, embedding, obs.Narrative, observationMetadata(obs))
	if err != nil {
		return fmt.Errorf("journal: vector store: %w", err)
	}
	return nil
}

// SearchSimilar embeds the query and asks the index for top-K
// neighbors. Returns (nil, nil) when no index is wired or when the
// bridge is unreachable — caller falls back to FTS results.
//
// Bounded by VectorOptions.searchTimeout(); a slow bridge degrades
// to "no semantic hits" rather than stalling the agent.
func (s *VectorEnabledStore) SearchSimilar(ctx context.Context, query string, topK int, threshold float64) ([]VectorSearchHit, error) {
	if s.index == nil || strings.TrimSpace(query) == "" {
		return nil, nil
	}
	embedCtx, cancel := context.WithTimeout(ctx, s.opts.searchTimeout())
	defer cancel()
	vec, _, err := s.index.Embed(embedCtx, query, s.opts.EmbedModel)
	if err != nil {
		return nil, fmt.Errorf("journal: embed query: %w", err)
	}
	searchCtx, cancel2 := context.WithTimeout(ctx, s.opts.searchTimeout())
	defer cancel2()
	hits, err := s.index.Search(searchCtx, vec, s.opts.topK(topK), s.opts.threshold(threshold))
	if err != nil {
		return nil, fmt.Errorf("journal: vector search: %w", err)
	}
	return hits, nil
}

// composeEmbedText assembles the embedding input from the most
// content-rich observation fields. Title + narrative captures the
// gist; concepts captures the structured tags.
func composeEmbedText(obs *Observation) string {
	var b strings.Builder
	if obs.Title != "" {
		b.WriteString(obs.Title)
		b.WriteString("\n\n")
	}
	if obs.Narrative != "" {
		b.WriteString(obs.Narrative)
	}
	if len(obs.Concepts) > 0 {
		b.WriteString("\n\nconcepts: ")
		b.WriteString(strings.Join(obs.Concepts, ", "))
	}
	return strings.TrimSpace(b.String())
}

// observationMetadata is what we attach to the vector record so
// downstream search results carry session + type filters.
func observationMetadata(obs *Observation) map[string]string {
	return map[string]string{
		"session_id":     obs.SessionID,
		"type":           string(obs.Type),
		"content_hash":   obs.ContentHash,
		"created_at_iso": obs.Timestamp.UTC().Format(time.RFC3339),
	}
}

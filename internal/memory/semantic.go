// Package memory — bridge-backed semantic recall (master plan §6.1).
//
// The Orchestrator can be augmented with an EmbeddingClient (typically a
// thin wrapper around bridge.Client) so that Remember computes an embedding
// and stores a vector, and SemanticRecall searches by query embedding with
// optional rerank. When no client is wired, the Orchestrator degrades to
// substring recall via the Badger store — semantic features are best-effort.
package memory

import (
	"context"
	"fmt"
)

// EmbeddingClient is the minimal embedding/vector/rerank surface the
// Orchestrator needs. bridge.Client satisfies this. Defined locally to keep
// the dependency direction one-way (memory doesn't import bridge).
type EmbeddingClient interface {
	Embed(ctx context.Context, text, model string) ([]float32, int32, error)
	StoreVector(ctx context.Context, id string, embedding []float32, content string, metadata map[string]string) (string, error)
	SearchVectors(ctx context.Context, query []float32, topK int, threshold float64) ([]VectorHit, error)
	Rerank(ctx context.Context, query string, documents []string, topN int) ([]RerankHit, error)
	DeleteVector(ctx context.Context, id string) (bool, error)
}

// VectorHit mirrors bridge.SearchResult without importing it.
type VectorHit struct {
	ID       string
	Score    float64
	Content  string
	Metadata map[string]string
}

// RerankHit mirrors bridge.RerankResult.
type RerankHit struct {
	Index int
	Score float64
	Text  string
}

// SemanticConfig controls embedding-backed behaviour.
type SemanticConfig struct {
	// EmbedModel is the model name passed to the embedding service. When
	// empty the orchestrator skips embedding entirely.
	EmbedModel string
	// SearchThreshold is the cosine-similarity floor for SearchVectors.
	SearchThreshold float64
	// RerankTopN, when > 0, runs results through Rerank and trims to N.
	RerankTopN int
}

// AttachEmbeddings wires an EmbeddingClient into the Orchestrator.
func (o *Orchestrator) AttachEmbeddings(client EmbeddingClient, cfg SemanticConfig) {
	o.embed = client
	o.semCfg = cfg
}

// HasEmbeddings reports whether semantic features are available.
func (o *Orchestrator) HasEmbeddings() bool {
	return o.embed != nil && o.semCfg.EmbedModel != ""
}

// RememberSemantic stores a memory in the local store AND computes/stores its
// embedding via the bridge. Falls back to plain Remember if no client is wired.
func (o *Orchestrator) RememberSemantic(ctx context.Context, content string, memType MemoryType, tags []string, sessionID string) (*Memory, error) {
	m := &Memory{
		Type:      memType,
		Content:   content,
		Tags:      append([]string(nil), tags...),
		SessionID: sessionID,
		Timestamp: timeNow(),
		Metadata:  map[string]string{},
	}
	if err := o.store.Store(ctx, m); err != nil {
		return nil, fmt.Errorf("memory: remember: %w", err)
	}
	if !o.HasEmbeddings() {
		return m, nil
	}
	emb, _, err := o.embed.Embed(ctx, content, o.semCfg.EmbedModel)
	if err != nil {
		// Best-effort: log via metadata, still return the stored memory.
		m.Metadata["embed_error"] = err.Error()
		return m, nil
	}
	meta := map[string]string{
		"type":       string(memType),
		"session_id": sessionID,
	}
	if _, err := o.embed.StoreVector(ctx, m.ID, emb, content, meta); err != nil {
		m.Metadata["vector_error"] = err.Error()
	}
	return m, nil
}

// SemanticRecall searches by embedding similarity, optionally reranks, and
// returns the corresponding Memory rows from the local store.
//
// When no client is wired this falls back to substring recall via Recall.
func (o *Orchestrator) SemanticRecall(ctx context.Context, query string, topK int) (*SearchResult, error) {
	if !o.HasEmbeddings() {
		return o.Recall(ctx, query, topK)
	}
	if topK <= 0 {
		topK = 10
	}
	qemb, _, err := o.embed.Embed(ctx, query, o.semCfg.EmbedModel)
	if err != nil {
		// Degrade rather than fail.
		return o.Recall(ctx, query, topK)
	}
	hits, err := o.embed.SearchVectors(ctx, qemb, topK, o.semCfg.SearchThreshold)
	if err != nil {
		return o.Recall(ctx, query, topK)
	}

	// Optional rerank.
	if o.semCfg.RerankTopN > 0 && len(hits) > 1 {
		docs := make([]string, len(hits))
		for i, h := range hits {
			docs[i] = h.Content
		}
		ranks, rerr := o.embed.Rerank(ctx, query, docs, o.semCfg.RerankTopN)
		if rerr == nil && len(ranks) > 0 {
			reordered := make([]VectorHit, 0, len(ranks))
			for _, r := range ranks {
				if r.Index >= 0 && r.Index < len(hits) {
					h := hits[r.Index]
					h.Score = r.Score
					reordered = append(reordered, h)
				}
			}
			hits = reordered
		}
	}

	memories := make([]Memory, 0, len(hits))
	for _, h := range hits {
		m, err := o.store.Get(ctx, h.ID)
		if err != nil || m == nil {
			continue
		}
		m.Relevance = h.Score
		memories = append(memories, *m)
	}
	return &SearchResult{Memories: memories, Total: len(memories)}, nil
}

// ForgetSemantic removes the memory from the local store and from the vector
// store (when wired). Vector deletion errors are swallowed (best-effort).
func (o *Orchestrator) ForgetSemantic(ctx context.Context, id string) error {
	if err := o.store.Delete(ctx, id); err != nil {
		return err
	}
	if o.HasEmbeddings() {
		_, _ = o.embed.DeleteVector(ctx, id)
	}
	return nil
}

package memory

import (
	"context"

	"github.com/Sahaj-Tech-ltd/ethos/bridge"
)

// BridgeAdapter wraps a *bridge.Client so it satisfies EmbeddingClient.
//
// memory does not import bridge directly to keep the dependency one-way and
// to allow tests to swap in a fake. Callers that already have a bridge
// client wrap it via NewBridgeAdapter at the wiring layer.
type BridgeAdapter struct {
	c *bridge.Client
}

// NewBridgeAdapter wraps c. Returns nil when c is nil.
func NewBridgeAdapter(c *bridge.Client) *BridgeAdapter {
	if c == nil {
		return nil
	}
	return &BridgeAdapter{c: c}
}

func (a *BridgeAdapter) Embed(ctx context.Context, text, model string) ([]float32, int32, error) {
	return a.c.Embed(ctx, text, model)
}

func (a *BridgeAdapter) StoreVector(ctx context.Context, id string, embedding []float32, content string, metadata map[string]string) (string, error) {
	return a.c.StoreVector(ctx, id, embedding, content, metadata)
}

func (a *BridgeAdapter) SearchVectors(ctx context.Context, query []float32, topK int, threshold float64) ([]VectorHit, error) {
	res, err := a.c.SearchVectors(ctx, query, topK, threshold)
	if err != nil {
		return nil, err
	}
	out := make([]VectorHit, len(res))
	for i, r := range res {
		out[i] = VectorHit{ID: r.ID, Score: r.Score, Content: r.Content, Metadata: r.Metadata}
	}
	return out, nil
}

func (a *BridgeAdapter) Rerank(ctx context.Context, query string, documents []string, topN int) ([]RerankHit, error) {
	res, err := a.c.Rerank(ctx, query, documents, topN)
	if err != nil {
		return nil, err
	}
	out := make([]RerankHit, len(res))
	for i, r := range res {
		out[i] = RerankHit{Index: r.Index, Score: r.Score, Text: r.Text}
	}
	return out, nil
}

func (a *BridgeAdapter) DeleteVector(ctx context.Context, id string) (bool, error) {
	return a.c.DeleteVector(ctx, id)
}

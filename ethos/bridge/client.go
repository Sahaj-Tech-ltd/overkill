package bridge

import (
	"context"

	pb "github.com/Sahaj-Tech-ltd/overkill/bridge/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type RerankResult struct {
	Index int
	Score float64
	Text  string
}

type SearchResult struct {
	ID       string
	Score    float64
	Content  string
	Metadata map[string]string
}

type Client struct {
	conn   *grpc.ClientConn
	client pb.OverkillBridgeClient
}

func NewClient(addr string) (*Client, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	return &Client{
		conn:   conn,
		client: pb.NewOverkillBridgeClient(conn),
	}, nil
}

func (c *Client) Close() error {
	return c.conn.Close()
}

func (c *Client) Ping(ctx context.Context) (string, error) {
	resp, err := c.client.Ping(ctx, &pb.PingRequest{})
	if err != nil {
		return "", err
	}
	return resp.GetStatus(), nil
}

func (c *Client) Embed(ctx context.Context, text string, model string) ([]float32, int32, error) {
	resp, err := c.client.Embed(ctx, &pb.EmbedRequest{Text: text, Model: model})
	if err != nil {
		return nil, 0, err
	}
	return resp.GetEmbedding(), resp.GetTokens(), nil
}

func (c *Client) EmbedBatch(ctx context.Context, texts []string, model string) ([][]float32, []int32, error) {
	resp, err := c.client.EmbedBatch(ctx, &pb.EmbedBatchRequest{Texts: texts, Model: model})
	if err != nil {
		return nil, nil, err
	}
	embeddings := make([][]float32, len(resp.GetResults()))
	tokens := make([]int32, len(resp.GetResults()))
	for i, r := range resp.GetResults() {
		embeddings[i] = r.GetEmbedding()
		tokens[i] = r.GetTokens()
	}
	return embeddings, tokens, nil
}

func (c *Client) Rerank(ctx context.Context, query string, documents []string, topN int) ([]RerankResult, error) {
	resp, err := c.client.Rerank(ctx, &pb.RerankRequest{
		Query:     query,
		Documents: documents,
		TopN:      int32(topN),
	})
	if err != nil {
		return nil, err
	}
	results := make([]RerankResult, len(resp.GetResults()))
	for i, r := range resp.GetResults() {
		results[i] = RerankResult{
			Index: int(r.GetIndex()),
			Score: r.GetRelevanceScore(),
			Text:  r.GetText(),
		}
	}
	return results, nil
}

func (c *Client) StoreVector(ctx context.Context, id string, embedding []float32, content string, metadata map[string]string) (string, error) {
	resp, err := c.client.StoreVector(ctx, &pb.StoreVectorRequest{
		Entry: &pb.VectorEntry{
			Id:        id,
			Embedding: embedding,
			Content:   content,
			Metadata:  metadata,
		},
	})
	if err != nil {
		return "", err
	}
	return resp.GetId(), nil
}

func (c *Client) SearchVectors(ctx context.Context, query []float32, topK int, threshold float64) ([]SearchResult, error) {
	resp, err := c.client.SearchVectors(ctx, &pb.SearchVectorsRequest{
		Query:     query,
		TopK:      int32(topK),
		Threshold: threshold,
	})
	if err != nil {
		return nil, err
	}
	results := make([]SearchResult, len(resp.GetResults()))
	for i, r := range resp.GetResults() {
		results[i] = SearchResult{
			ID:       r.GetId(),
			Score:    r.GetScore(),
			Content:  r.GetContent(),
			Metadata: r.GetMetadata(),
		}
	}
	return results, nil
}

func (c *Client) DeleteVector(ctx context.Context, id string) (bool, error) {
	resp, err := c.client.DeleteVector(ctx, &pb.DeleteVectorRequest{Id: id})
	if err != nil {
		return false, err
	}
	return resp.GetSuccess(), nil
}

func (c *Client) Compact(ctx context.Context, content string, model string, targetTokens int, style string) (string, int32, int32, error) {
	resp, err := c.client.Compact(ctx, &pb.CompactRequest{
		Content:      content,
		Model:        model,
		TargetTokens: int32(targetTokens),
		Style:        style,
	})
	if err != nil {
		return "", 0, 0, err
	}
	return resp.GetSummary(), resp.GetOriginalTokens(), resp.GetSummaryTokens(), nil
}

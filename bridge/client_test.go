package bridge

import (
	"context"
	"net"
	"testing"

	pb "github.com/Sahaj-Tech-ltd/overkill/bridge/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type mockServicer struct {
	pb.UnimplementedOverkillBridgeServer
}

func (m *mockServicer) Ping(_ context.Context, _ *pb.PingRequest) (*pb.PongResponse, error) {
	return &pb.PongResponse{Status: "ok", Version: "test"}, nil
}

func (m *mockServicer) Embed(_ context.Context, req *pb.EmbedRequest) (*pb.EmbedResponse, error) {
	return &pb.EmbedResponse{
		Embedding: []float32{0.1, 0.2, 0.3},
		Tokens:    int32(len(req.Text) / 4),
	}, nil
}

func (m *mockServicer) EmbedBatch(_ context.Context, req *pb.EmbedBatchRequest) (*pb.EmbedBatchResponse, error) {
	results := make([]*pb.EmbedResult, len(req.Texts))
	for i := range req.Texts {
		results[i] = &pb.EmbedResult{Embedding: []float32{0.1, 0.2, 0.3}, Tokens: 1}
	}
	return &pb.EmbedBatchResponse{Results: results}, nil
}

func (m *mockServicer) Rerank(_ context.Context, req *pb.RerankRequest) (*pb.RerankResponse, error) {
	results := make([]*pb.RerankResult, len(req.Documents))
	for i, doc := range req.Documents {
		results[i] = &pb.RerankResult{
			Index:          int32(i),
			RelevanceScore: float64(len(req.Documents)-i) / float64(len(req.Documents)),
			Text:           doc,
		}
	}
	return &pb.RerankResponse{Results: results}, nil
}

func (m *mockServicer) StoreVector(_ context.Context, req *pb.StoreVectorRequest) (*pb.StoreVectorResponse, error) {
	return &pb.StoreVectorResponse{Id: req.Entry.Id, Success: true}, nil
}

func (m *mockServicer) SearchVectors(_ context.Context, _ *pb.SearchVectorsRequest) (*pb.SearchVectorsResponse, error) {
	return &pb.SearchVectorsResponse{
		Results: []*pb.SearchResult{
			{Id: "v1", Score: 0.95, Content: "hello world", Metadata: map[string]string{"type": "doc"}},
		},
	}, nil
}

func (m *mockServicer) DeleteVector(_ context.Context, _ *pb.DeleteVectorRequest) (*pb.DeleteVectorResponse, error) {
	return &pb.DeleteVectorResponse{Success: true}, nil
}

func (m *mockServicer) Compact(_ context.Context, req *pb.CompactRequest) (*pb.CompactResponse, error) {
	return &pb.CompactResponse{
		Summary:        req.Content[:min(len(req.Content), 100)],
		OriginalTokens: int32(len(req.Content) / 4),
		SummaryTokens:  int32(min(len(req.Content), 100) / 4),
		Success:        true,
	}, nil
}

func startMockServer(t *testing.T) (*grpc.Server, string) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	srv := grpc.NewServer()
	pb.RegisterOverkillBridgeServer(srv, &mockServicer{})
	go srv.Serve(lis)
	return srv, lis.Addr().String()
}

func newTestClient(t *testing.T, addr string) *Client {
	t.Helper()
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("failed to dial: %v", err)
	}
	return &Client{
		conn:   conn,
		client: pb.NewOverkillBridgeClient(conn),
	}
}

func TestClient_Ping(t *testing.T) {
	srv, addr := startMockServer(t)
	defer srv.Stop()

	client := newTestClient(t, addr)
	defer client.Close()

	ctx := context.Background()
	status, err := client.Ping(ctx)
	if err != nil {
		t.Fatalf("Ping failed: %v", err)
	}
	if status != "ok" {
		t.Errorf("expected status ok, got %s", status)
	}
}

func TestClient_Embed(t *testing.T) {
	srv, addr := startMockServer(t)
	defer srv.Stop()

	client := newTestClient(t, addr)
	defer client.Close()

	ctx := context.Background()
	emb, tokens, err := client.Embed(ctx, "hello world", "test-model")
	if err != nil {
		t.Fatalf("Embed failed: %v", err)
	}
	if len(emb) != 3 {
		t.Errorf("expected 3 floats, got %d", len(emb))
	}
	if tokens <= 0 {
		t.Errorf("expected positive tokens, got %d", tokens)
	}
}

func TestClient_EmbedBatch(t *testing.T) {
	srv, addr := startMockServer(t)
	defer srv.Stop()

	client := newTestClient(t, addr)
	defer client.Close()

	ctx := context.Background()
	embeddings, tokens, err := client.EmbedBatch(ctx, []string{"hello", "world"}, "test-model")
	if err != nil {
		t.Fatalf("EmbedBatch failed: %v", err)
	}
	if len(embeddings) != 2 {
		t.Errorf("expected 2 results, got %d", len(embeddings))
	}
	if len(tokens) != 2 {
		t.Errorf("expected 2 token counts, got %d", len(tokens))
	}
}

func TestClient_Rerank(t *testing.T) {
	srv, addr := startMockServer(t)
	defer srv.Stop()

	client := newTestClient(t, addr)
	defer client.Close()

	ctx := context.Background()
	results, err := client.Rerank(ctx, "test query", []string{"doc1", "doc2", "doc3"}, 2)
	if err != nil {
		t.Fatalf("Rerank failed: %v", err)
	}
	if len(results) != 3 {
		t.Errorf("expected 3 results, got %d", len(results))
	}
}

func TestClient_StoreSearchDeleteVector(t *testing.T) {
	srv, addr := startMockServer(t)
	defer srv.Stop()

	client := newTestClient(t, addr)
	defer client.Close()

	ctx := context.Background()

	id, err := client.StoreVector(ctx, "v1", []float32{0.1, 0.2, 0.3}, "test content", map[string]string{"type": "doc"})
	if err != nil {
		t.Fatalf("StoreVector failed: %v", err)
	}
	if id != "v1" {
		t.Errorf("expected id v1, got %s", id)
	}

	results, err := client.SearchVectors(ctx, []float32{0.1, 0.2, 0.3}, 5, 0.5)
	if err != nil {
		t.Fatalf("SearchVectors failed: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("expected 1 result, got %d", len(results))
	}
	if results[0].ID != "v1" {
		t.Errorf("expected id v1, got %s", results[0].ID)
	}

	deleted, err := client.DeleteVector(ctx, "v1")
	if err != nil {
		t.Fatalf("DeleteVector failed: %v", err)
	}
	if !deleted {
		t.Error("expected delete to succeed")
	}
}

func TestClient_Compact(t *testing.T) {
	srv, addr := startMockServer(t)
	defer srv.Stop()

	client := newTestClient(t, addr)
	defer client.Close()

	ctx := context.Background()
	content := "This is a long piece of text that needs to be compacted into a shorter summary for testing purposes."
	summary, origTokens, summaryTokens, err := client.Compact(ctx, content, "test-model", 50, "detailed")
	if err != nil {
		t.Fatalf("Compact failed: %v", err)
	}
	if summary == "" {
		t.Error("expected non-empty summary")
	}
	if origTokens <= 0 {
		t.Errorf("expected positive original tokens, got %d", origTokens)
	}
	if summaryTokens < 0 {
		t.Errorf("expected non-negative summary tokens, got %d", summaryTokens)
	}
}

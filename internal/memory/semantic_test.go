package memory

import (
	"context"
	"errors"
	"testing"

	"github.com/dgraph-io/badger/v4"
)

type fakeEmbed struct {
	embed       func(text string) ([]float32, error)
	stored      []storedVec
	storeErr    error
	searchHits  []VectorHit
	searchErr   error
	rerankHits  []RerankHit
	rerankCalls int
	deletes     []string
}

type storedVec struct {
	id      string
	content string
	emb     []float32
}

func (f *fakeEmbed) Embed(ctx context.Context, text, model string) ([]float32, int32, error) {
	if f.embed != nil {
		v, err := f.embed(text)
		return v, int32(len(text)), err
	}
	return []float32{1, 2, 3}, int32(len(text)), nil
}
func (f *fakeEmbed) StoreVector(ctx context.Context, id string, embedding []float32, content string, metadata map[string]string) (string, error) {
	if f.storeErr != nil {
		return "", f.storeErr
	}
	f.stored = append(f.stored, storedVec{id: id, content: content, emb: embedding})
	return id, nil
}
func (f *fakeEmbed) SearchVectors(ctx context.Context, query []float32, topK int, threshold float64) ([]VectorHit, error) {
	if f.searchErr != nil {
		return nil, f.searchErr
	}
	return f.searchHits, nil
}
func (f *fakeEmbed) Rerank(ctx context.Context, query string, documents []string, topN int) ([]RerankHit, error) {
	f.rerankCalls++
	return f.rerankHits, nil
}
func (f *fakeEmbed) DeleteVector(ctx context.Context, id string) (bool, error) {
	f.deletes = append(f.deletes, id)
	return true, nil
}

func newOrchWithStore(t *testing.T) *Orchestrator {
	t.Helper()
	dir := t.TempDir()
	db, err := badger.Open(badger.DefaultOptions(dir).WithLoggingLevel(badger.ERROR))
	if err != nil {
		t.Fatalf("badger open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	store := NewBadgerStore(db)
	return NewOrchestrator(store, nil, "")
}

func TestOrchestrator_RememberSemantic_StoresVectorWhenWired(t *testing.T) {
	o := newOrchWithStore(t)
	fe := &fakeEmbed{}
	o.AttachEmbeddings(fe, SemanticConfig{EmbedModel: "test-embed"})
	if !o.HasEmbeddings() {
		t.Fatal("HasEmbeddings should be true after AttachEmbeddings")
	}
	m, err := o.RememberSemantic(context.Background(), "the cat sat on the mat", MemorySemantic, []string{"cats"}, "sess1")
	if err != nil {
		t.Fatalf("remember: %v", err)
	}
	if len(fe.stored) != 1 {
		t.Fatalf("stored = %d want 1", len(fe.stored))
	}
	if fe.stored[0].id != m.ID {
		t.Fatalf("stored id = %q want %q", fe.stored[0].id, m.ID)
	}
}

func TestOrchestrator_RememberSemantic_BestEffortOnEmbedError(t *testing.T) {
	o := newOrchWithStore(t)
	fe := &fakeEmbed{embed: func(string) ([]float32, error) { return nil, errors.New("network down") }}
	o.AttachEmbeddings(fe, SemanticConfig{EmbedModel: "x"})
	m, err := o.RememberSemantic(context.Background(), "content", MemorySemantic, nil, "s")
	if err != nil {
		t.Fatalf("expected non-fatal embed error, got %v", err)
	}
	if m.Metadata["embed_error"] == "" {
		t.Fatal("expected embed_error annotation in metadata")
	}
}

func TestOrchestrator_SemanticRecall_ReturnsHitsFromStore(t *testing.T) {
	o := newOrchWithStore(t)
	// Seed two memories.
	m1, _ := o.RememberSemantic(context.Background(), "alpha content", MemorySemantic, nil, "s")
	m2, _ := o.RememberSemantic(context.Background(), "beta content", MemorySemantic, nil, "s")

	fe := &fakeEmbed{
		searchHits: []VectorHit{
			{ID: m2.ID, Score: 0.9, Content: m2.Content},
			{ID: m1.ID, Score: 0.7, Content: m1.Content},
		},
	}
	o.AttachEmbeddings(fe, SemanticConfig{EmbedModel: "x"})

	res, err := o.SemanticRecall(context.Background(), "query", 5)
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	if len(res.Memories) != 2 {
		t.Fatalf("got %d memories want 2", len(res.Memories))
	}
	if res.Memories[0].ID != m2.ID {
		t.Fatalf("first hit = %q want %q (preserve search order)", res.Memories[0].ID, m2.ID)
	}
	if res.Memories[0].Relevance != 0.9 {
		t.Fatalf("relevance = %v want 0.9", res.Memories[0].Relevance)
	}
}

func TestOrchestrator_SemanticRecall_RerankReorders(t *testing.T) {
	o := newOrchWithStore(t)
	m1, _ := o.RememberSemantic(context.Background(), "alpha", MemorySemantic, nil, "s")
	m2, _ := o.RememberSemantic(context.Background(), "beta", MemorySemantic, nil, "s")

	fe := &fakeEmbed{
		searchHits: []VectorHit{
			{ID: m1.ID, Score: 0.5, Content: "alpha"},
			{ID: m2.ID, Score: 0.4, Content: "beta"},
		},
		rerankHits: []RerankHit{
			{Index: 1, Score: 0.99, Text: "beta"},
			{Index: 0, Score: 0.20, Text: "alpha"},
		},
	}
	o.AttachEmbeddings(fe, SemanticConfig{EmbedModel: "x", RerankTopN: 2})
	res, _ := o.SemanticRecall(context.Background(), "q", 5)
	if fe.rerankCalls != 1 {
		t.Fatalf("rerank not called")
	}
	if res.Memories[0].ID != m2.ID {
		t.Fatalf("rerank reordering failed: %q first", res.Memories[0].ID)
	}
}

func TestOrchestrator_SemanticRecall_FallsBackWhenEmbedFails(t *testing.T) {
	o := newOrchWithStore(t)
	_, _ = o.RememberSemantic(context.Background(), "alpha content", MemorySemantic, nil, "s")
	fe := &fakeEmbed{embed: func(string) ([]float32, error) { return nil, errors.New("down") }}
	o.AttachEmbeddings(fe, SemanticConfig{EmbedModel: "x"})

	// Should not error — falls back to substring Recall.
	res, err := o.SemanticRecall(context.Background(), "alpha", 5)
	if err != nil {
		t.Fatalf("expected fallback ok, got %v", err)
	}
	if res == nil {
		t.Fatal("nil result")
	}
}

func TestOrchestrator_NoEmbed_FallsBackToRecall(t *testing.T) {
	o := newOrchWithStore(t)
	_ = o.Remember(context.Background(), "alpha", MemorySemantic, nil, "s")
	res, err := o.SemanticRecall(context.Background(), "alpha", 5)
	if err != nil {
		t.Fatalf("recall: %v", err)
	}
	_ = res
}

func TestOrchestrator_ForgetSemantic_DeletesFromBoth(t *testing.T) {
	o := newOrchWithStore(t)
	fe := &fakeEmbed{}
	o.AttachEmbeddings(fe, SemanticConfig{EmbedModel: "x"})
	m, _ := o.RememberSemantic(context.Background(), "x", MemorySemantic, nil, "s")
	if err := o.ForgetSemantic(context.Background(), m.ID); err != nil {
		t.Fatalf("forget: %v", err)
	}
	if len(fe.deletes) != 1 || fe.deletes[0] != m.ID {
		t.Fatalf("deletes = %v want [%s]", fe.deletes, m.ID)
	}
}

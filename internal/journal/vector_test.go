package journal

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

type stubIndex struct {
	embedCalls  int
	storeCalls  int
	searchCalls int
	lastStoreID string
	lastQuery   []float32

	embedFn  func(text string) ([]float32, error)
	storeFn  func(id string, vec []float32, content string, meta map[string]string) error
	searchFn func(vec []float32, topK int) ([]VectorSearchHit, error)
}

func (s *stubIndex) Embed(_ context.Context, text, _ string) ([]float32, int32, error) {
	s.embedCalls++
	if s.embedFn != nil {
		v, err := s.embedFn(text)
		return v, int32(len(text)), err
	}
	return []float32{0.1, 0.2, 0.3}, int32(len(text)), nil
}

func (s *stubIndex) Store(_ context.Context, id string, vec []float32, content string, meta map[string]string) (string, error) {
	s.storeCalls++
	s.lastStoreID = id
	if s.storeFn != nil {
		if err := s.storeFn(id, vec, content, meta); err != nil {
			return "", err
		}
	}
	return id, nil
}

func (s *stubIndex) Search(_ context.Context, vec []float32, topK int, _ float64) ([]VectorSearchHit, error) {
	s.searchCalls++
	s.lastQuery = vec
	if s.searchFn != nil {
		return s.searchFn(vec, topK)
	}
	return nil, nil
}

func newObs(t *testing.T) *Observation {
	t.Helper()
	o := NewObservation(ObsBugfix, "fix cache stampede", "redis miss → DB pile-up under cold start", "sess-1")
	o.Concepts = []string{"redis", "cache", "performance"}
	return o
}

func TestVectorEnabledStore_StoreWithoutIndexFallsThrough(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "obs")
	store := NewObservationStore(dir)
	vs := NewVectorEnabledStore(store, nil, VectorOptions{})

	obs := newObs(t)
	if err := vs.StoreWithVector(context.Background(), obs); err != nil {
		t.Fatal(err)
	}
	// File-based persistence still happens — load and verify.
	all := vs.List("", 0)
	if len(all) != 1 {
		t.Errorf("nil index should still persist observation, got %d", len(all))
	}
}

func TestVectorEnabledStore_StoreWithIndexCallsEmbedAndStore(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "obs")
	idx := &stubIndex{}
	vs := NewVectorEnabledStore(NewObservationStore(dir), idx, VectorOptions{})

	obs := newObs(t)
	if err := vs.StoreWithVector(context.Background(), obs); err != nil {
		t.Fatal(err)
	}
	if idx.embedCalls != 1 {
		t.Errorf("expected 1 embed call, got %d", idx.embedCalls)
	}
	if idx.storeCalls != 1 {
		t.Errorf("expected 1 store call, got %d", idx.storeCalls)
	}
	if idx.lastStoreID != obs.ID {
		t.Errorf("expected store id %s, got %s", obs.ID, idx.lastStoreID)
	}
}

func TestVectorEnabledStore_EmbedFailureStillPersists(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "obs")
	idx := &stubIndex{
		embedFn: func(string) ([]float32, error) { return nil, errors.New("bridge down") },
	}
	vs := NewVectorEnabledStore(NewObservationStore(dir), idx, VectorOptions{})

	obs := newObs(t)
	err := vs.StoreWithVector(context.Background(), obs)
	if err == nil {
		t.Fatal("expected embed error to surface")
	}
	// Disk persistence must still have happened — the embed failure
	// is reported but the durability invariant holds.
	all := vs.List("", 0)
	if len(all) != 1 {
		t.Errorf("durability invariant violated: %d observations on disk", len(all))
	}
}

func TestVectorEnabledStore_SearchSimilar_NoIndexReturnsNil(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "obs")
	vs := NewVectorEnabledStore(NewObservationStore(dir), nil, VectorOptions{})

	hits, err := vs.SearchSimilar(context.Background(), "cache miss redis", 5, 0.5)
	if err != nil {
		t.Fatal(err)
	}
	if hits != nil {
		t.Errorf("nil index → nil hits, got %+v", hits)
	}
}

func TestVectorEnabledStore_SearchSimilarPipesThroughIndex(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "obs")
	idx := &stubIndex{
		searchFn: func(vec []float32, k int) ([]VectorSearchHit, error) {
			return []VectorSearchHit{
				{ID: "a", Score: 0.92, Content: "neighbor a"},
				{ID: "b", Score: 0.81, Content: "neighbor b"},
			}, nil
		},
	}
	vs := NewVectorEnabledStore(NewObservationStore(dir), idx, VectorOptions{DefaultTopK: 3})

	hits, err := vs.SearchSimilar(context.Background(), "redis cache stampede", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 || hits[0].ID != "a" {
		t.Errorf("unexpected hits: %+v", hits)
	}
	if idx.embedCalls != 1 {
		t.Errorf("query should be embedded once, got %d", idx.embedCalls)
	}
}

func TestVectorEnabledStore_SearchSimilarEmptyQueryIsNil(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "obs")
	idx := &stubIndex{}
	vs := NewVectorEnabledStore(NewObservationStore(dir), idx, VectorOptions{})

	hits, _ := vs.SearchSimilar(context.Background(), "   ", 5, 0.5)
	if hits != nil {
		t.Errorf("empty query → nil hits, got %+v", hits)
	}
	if idx.embedCalls != 0 {
		t.Error("empty query should not hit the embedder")
	}
}

func TestComposeEmbedText_TitleNarrativeConcepts(t *testing.T) {
	obs := &Observation{
		Title:     "fix auth",
		Narrative: "csrf token was unscoped",
		Concepts:  []string{"auth", "csrf"},
	}
	got := composeEmbedText(obs)
	if got == "" {
		t.Fatal("expected non-empty embed text")
	}
	for _, s := range []string{"fix auth", "csrf token", "concepts:", "auth, csrf"} {
		if !contains(got, s) {
			t.Errorf("missing %q in %s", s, got)
		}
	}
}

func TestVectorOptions_Defaults(t *testing.T) {
	var o VectorOptions
	if o.topK(0) != 10 {
		t.Errorf("default topK should be 10, got %d", o.topK(0))
	}
	if o.threshold(0) != 0.5 {
		t.Errorf("default threshold 0.5, got %v", o.threshold(0))
	}
	if o.indexTimeout() != 3*time.Second {
		t.Errorf("default index timeout 3s, got %v", o.indexTimeout())
	}
	if o.searchTimeout() != 5*time.Second {
		t.Errorf("default search timeout 5s, got %v", o.searchTimeout())
	}
}

// contains is a tiny avoid-strings.Contains shim so this test doesn't
// duplicate the package's import list.
func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

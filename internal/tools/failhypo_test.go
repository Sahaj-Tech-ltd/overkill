package tools

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/journal"
)

type stubFHStore struct {
	records []journal.FailedHypothesis
}

func (s *stubFHStore) Search(query string) ([]journal.FailedHypothesis, error) {
	var out []journal.FailedHypothesis
	for _, r := range s.records {
		if query == "" {
			out = append(out, r)
			continue
		}
		if containsFold(r.Subject, query) || containsFold(r.Hypothesis, query) || containsFold(r.Reason, query) {
			out = append(out, r)
		}
	}
	return out, nil
}

func (s *stubFHStore) All() ([]journal.FailedHypothesis, error) {
	return append([]journal.FailedHypothesis(nil), s.records...), nil
}

func (s *stubFHStore) SearchForModel(query, modelID string) ([]journal.FailedHypothesis, error) {
	hits, err := s.Search(query)
	if err != nil || modelID == "" {
		return hits, err
	}
	out := hits[:0]
	for _, h := range hits {
		if h.ModelID == "" || h.ModelID == modelID {
			out = append(out, h)
		}
	}
	return out, nil
}

type stubModelProvider struct{ id string }

func (s stubModelProvider) Model() string { return s.id }

func containsFold(s, q string) bool {
	if len(q) > len(s) {
		return false
	}
	for i := 0; i+len(q) <= len(s); i++ {
		match := true
		for j := 0; j < len(q); j++ {
			a := s[i+j]
			b := q[j]
			if 'A' <= a && a <= 'Z' {
				a += 32
			}
			if 'A' <= b && b <= 'Z' {
				b += 32
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func newStubWith(records ...journal.FailedHypothesis) *stubFHStore {
	return &stubFHStore{records: records}
}

func TestFailHypoSearch_QueryFiltersAndLimitsAndReverses(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	store := newStubWith(
		journal.FailedHypothesis{ID: "1", Subject: "auth", Hypothesis: "reorder middleware", Reason: "null cookie", Timestamp: t0},
		journal.FailedHypothesis{ID: "2", Subject: "cache", Hypothesis: "redis layer", Reason: "miss rate high", Timestamp: t0.Add(time.Hour)},
		journal.FailedHypothesis{ID: "3", Subject: "auth", Hypothesis: "swap jwt lib", Reason: "kid header missing", Timestamp: t0.Add(2 * time.Hour)},
	)

	tool := NewFailHypoSearchTool(store)
	in, _ := json.Marshal(map[string]any{"query": "auth"})
	raw, err := tool.Execute(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	var resp struct {
		Hits  []journal.FailedHypothesis `json:"hits"`
		Count int                        `json:"count"`
		Query string                     `json:"query"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Count != 2 {
		t.Fatalf("expected 2 auth hits, got %d (%+v)", resp.Count, resp.Hits)
	}
	if resp.Hits[0].ID != "3" || resp.Hits[1].ID != "1" {
		t.Errorf("expected newest-first ordering, got %s then %s", resp.Hits[0].ID, resp.Hits[1].ID)
	}
	if resp.Query != "auth" {
		t.Errorf("query echo missing")
	}
}

func TestFailHypoSearch_EmptyQueryReturnsAll(t *testing.T) {
	store := newStubWith(
		journal.FailedHypothesis{ID: "1", Hypothesis: "a"},
		journal.FailedHypothesis{ID: "2", Hypothesis: "b"},
	)
	tool := NewFailHypoSearchTool(store)
	raw, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var resp struct {
		Count int `json:"count"`
	}
	_ = json.Unmarshal(raw, &resp)
	if resp.Count != 2 {
		t.Errorf("empty query should return all, got %d", resp.Count)
	}
}

func TestFailHypoSearch_LimitApplied(t *testing.T) {
	var recs []journal.FailedHypothesis
	for i := 0; i < 25; i++ {
		recs = append(recs, journal.FailedHypothesis{ID: "r", Hypothesis: "x"})
	}
	store := newStubWith(recs...)
	tool := NewFailHypoSearchTool(store)
	in, _ := json.Marshal(map[string]any{"limit": 5})
	raw, _ := tool.Execute(context.Background(), in)
	var resp struct {
		Count int `json:"count"`
	}
	_ = json.Unmarshal(raw, &resp)
	if resp.Count != 5 {
		t.Errorf("limit=5 should cap, got %d", resp.Count)
	}
}

func TestFailHypoSearch_AutoFiltersToCurrentModel(t *testing.T) {
	store := newStubWith(
		journal.FailedHypothesis{ID: "1", ModelID: "claude-opus-4-7", Hypothesis: "a"},
		journal.FailedHypothesis{ID: "2", ModelID: "gpt-5.4", Hypothesis: "b"},
		journal.FailedHypothesis{ID: "3", ModelID: "", Hypothesis: "c"}, // unversioned record passes
	)
	tool := NewFailHypoSearchTool(store).WithCurrentModel(stubModelProvider{id: "claude-opus-4-7"})

	raw, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var resp struct {
		Hits []journal.FailedHypothesis `json:"hits"`
	}
	_ = json.Unmarshal(raw, &resp)
	ids := map[string]bool{}
	for _, h := range resp.Hits {
		ids[h.ID] = true
	}
	if ids["2"] {
		t.Errorf("gpt-5.4 record should be filtered out: %+v", resp.Hits)
	}
	if !ids["1"] || !ids["3"] {
		t.Errorf("current-model and unversioned records should be included: %+v", resp.Hits)
	}
}

func TestFailHypoSearch_StarOptsOutOfModelFilter(t *testing.T) {
	store := newStubWith(
		journal.FailedHypothesis{ID: "1", ModelID: "claude-opus-4-7", Hypothesis: "a"},
		journal.FailedHypothesis{ID: "2", ModelID: "gpt-5.4", Hypothesis: "b"},
	)
	tool := NewFailHypoSearchTool(store).WithCurrentModel(stubModelProvider{id: "claude-opus-4-7"})

	in, _ := json.Marshal(map[string]any{"model_id": "*"})
	raw, _ := tool.Execute(context.Background(), in)
	var resp struct {
		Count int `json:"count"`
	}
	_ = json.Unmarshal(raw, &resp)
	if resp.Count != 2 {
		t.Errorf("model_id=* should return both records, got %d", resp.Count)
	}
}

func TestFailHypoSearch_NilQuerierReturnsErrorJSON(t *testing.T) {
	tool := NewFailHypoSearchTool(nil)
	raw, err := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) == 0 {
		t.Error("expected error envelope, got empty")
	}
}

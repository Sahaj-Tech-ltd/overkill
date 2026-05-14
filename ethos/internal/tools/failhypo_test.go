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

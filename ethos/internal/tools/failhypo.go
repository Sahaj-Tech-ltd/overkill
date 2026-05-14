// Package tools — failhypo_search exposes the failed-hypothesis store
// (internal/journal/failhypo.go) to the agent.
//
// Why the agent needs this: the store is most valuable when the
// agent queries it BEFORE retrying a known-dead path, not when a
// human runs `overkill failhypo search` afterwards. One extra
// tool_call mid-turn is cheap; re-doing a 20-minute debug loop is
// not. This is the §4.19 layered-disclosure pattern applied to the
// failure record instead of the flight log.
package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Sahaj-Tech-ltd/overkill/internal/journal"
)

// FailHypoQuerier is the minimal surface the tool needs from a real
// *journal.FailedHypothesisStore. Local so internal/tools doesn't
// take a hard dep on the concrete store type.
type FailHypoQuerier interface {
	Search(query string) ([]journal.FailedHypothesis, error)
	All() ([]journal.FailedHypothesis, error)
}

type FailHypoSearchTool struct {
	q FailHypoQuerier
}

func NewFailHypoSearchTool(q FailHypoQuerier) *FailHypoSearchTool {
	return &FailHypoSearchTool{q: q}
}

func (t *FailHypoSearchTool) Name() string { return "failhypo_search" }

type failhypoSearchInput struct {
	// Query: substring matched (case-insensitive) against subject,
	// hypothesis, and reason. Empty query returns the full store —
	// useful for "what have we tried lately?" rather than a targeted
	// lookup.
	Query string `json:"query,omitempty"`

	// Limit caps the returned record count. Defaults to 20 — the
	// agent rarely needs more, and the prompt cost adds up fast.
	Limit int `json:"limit,omitempty"`
}

func (t *FailHypoSearchTool) Execute(_ context.Context, in json.RawMessage) (json.RawMessage, error) {
	if t.q == nil {
		return errorJSON("failhypo store not configured"), nil
	}
	var req failhypoSearchInput
	if len(in) > 0 {
		if err := json.Unmarshal(in, &req); err != nil {
			return nil, fmt.Errorf("failhypo_search: %w", err)
		}
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 20
	}

	var (
		hits []journal.FailedHypothesis
		err  error
	)
	if req.Query == "" {
		hits, err = t.q.All()
	} else {
		hits, err = t.q.Search(req.Query)
	}
	if err != nil {
		return errorJSON(err.Error()), nil
	}

	// Return newest-first so the agent reads the most recent failures
	// (most likely still relevant) before older ones. Cheap reverse —
	// store appends in chronological order.
	for i, j := 0, len(hits)-1; i < j; i, j = i+1, j-1 {
		hits[i], hits[j] = hits[j], hits[i]
	}
	if len(hits) > limit {
		hits = hits[:limit]
	}

	body, _ := json.Marshal(map[string]any{
		"hits":  hits,
		"count": len(hits),
		"query": req.Query,
	})
	return body, nil
}

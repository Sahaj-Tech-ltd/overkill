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
	SearchForModel(query, modelID string) ([]journal.FailedHypothesis, error)
	All() ([]journal.FailedHypothesis, error)
}

// CurrentModelProvider lets the tool ask for the model that's
// active RIGHT NOW so default-on filtering applies. Optional —
// nil disables auto-filter (returns everything).
type CurrentModelProvider interface {
	Model() string
}

type FailHypoSearchTool struct {
	q FailHypoQuerier
	m CurrentModelProvider // optional; nil = no auto-filter
}

func NewFailHypoSearchTool(q FailHypoQuerier) *FailHypoSearchTool {
	return &FailHypoSearchTool{q: q}
}

// WithCurrentModel attaches a model provider so the tool defaults
// to filtering hits to the active model. Callers can still pass
// model_id="*" in the request to opt out and see every model.
func (t *FailHypoSearchTool) WithCurrentModel(m CurrentModelProvider) *FailHypoSearchTool {
	t.m = m
	return t
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

	// ModelID restricts results to records produced by this model.
	// Empty (default) AND a current-model provider is wired → the
	// tool auto-filters to the active model. Pass "*" to disable
	// filtering and see records from every model. §4.16.
	ModelID string `json:"model_id,omitempty"`
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

	// Resolve the effective model filter. Explicit "*" → no filter.
	// Empty + current-model provider wired → active model. Empty
	// + no provider → no filter (mirrors prior behaviour).
	modelFilter := req.ModelID
	if modelFilter == "*" {
		modelFilter = ""
	} else if modelFilter == "" && t.m != nil {
		modelFilter = t.m.Model()
	}

	var (
		hits []journal.FailedHypothesis
		err  error
	)
	switch {
	case req.Query == "" && modelFilter == "":
		hits, err = t.q.All()
	case modelFilter == "":
		hits, err = t.q.Search(req.Query)
	default:
		hits, err = t.q.SearchForModel(req.Query, modelFilter)
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

// Package tools — record_learning + learnings_search.
//
// At end-of-task the agent calls record_learning with a one-line
// lesson + optional topic/tags. The store is append-only JSONL;
// the agent CANNOT remove or overwrite prior learnings via the
// tool surface. Combined with the protected-path gate (raw writes
// to the learnings dir blocked) this is the durability story the
// user asked for: no single hallucinated turn can wipe history.

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Sahaj-Tech-ltd/overkill/internal/plan"
)

// LearningsQuerier is the minimal surface the tools need.
type LearningsQuerier interface {
	Append(l plan.Learning) error
	All() ([]plan.Learning, error)
	Search(query string) ([]plan.Learning, error)
	SearchForModel(query, modelID string) ([]plan.Learning, error)
}

// SessionIDProvider lets the tool tag records with the active
// session ID without callers having to thread it manually.
type SessionIDProvider interface {
	SessionID() string
}

// ── record_learning ─────────────────────────────────────────────────

type RecordLearningTool struct {
	q LearningsQuerier
	s SessionIDProvider
	m CurrentModelProvider // optional; nil = no model tagging
}

func NewRecordLearningTool(q LearningsQuerier, s SessionIDProvider) *RecordLearningTool {
	return &RecordLearningTool{q: q, s: s}
}

// WithCurrentModel attaches a model provider so each recorded
// learning is tagged with the active model (§4.16).
func (t *RecordLearningTool) WithCurrentModel(m CurrentModelProvider) *RecordLearningTool {
	t.m = m
	return t
}

func (t *RecordLearningTool) Name() string { return "record_learning" }

type recordLearningInput struct {
	Topic  string   `json:"topic"`
	Lesson string   `json:"lesson"`
	Tags   []string `json:"tags,omitempty"`
}

func (t *RecordLearningTool) Execute(_ context.Context, in json.RawMessage) (json.RawMessage, error) {
	if t.q == nil {
		return errorJSON("learnings store not configured"), nil
	}
	var req recordLearningInput
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, fmt.Errorf("record_learning: %w", err)
	}
	if strings.TrimSpace(req.Lesson) == "" {
		return errorJSON("record_learning: 'lesson' is required"), nil
	}
	l := plan.Learning{
		Topic:  strings.TrimSpace(req.Topic),
		Lesson: strings.TrimSpace(req.Lesson),
		Tags:   req.Tags,
	}
	if t.s != nil {
		l.SessionID = t.s.SessionID()
	}
	if t.m != nil {
		l.ModelID = t.m.Model()
	}
	if err := t.q.Append(l); err != nil {
		return errorJSON(err.Error()), nil
	}
	return json.Marshal(map[string]any{
		"ok":       true,
		"learning": l,
	})
}

// ── learnings_search ────────────────────────────────────────────────

type LearningsSearchTool struct {
	q LearningsQuerier
	m CurrentModelProvider
}

func NewLearningsSearchTool(q LearningsQuerier) *LearningsSearchTool {
	return &LearningsSearchTool{q: q}
}

func (t *LearningsSearchTool) WithCurrentModel(m CurrentModelProvider) *LearningsSearchTool {
	t.m = m
	return t
}

func (t *LearningsSearchTool) Name() string { return "learnings_search" }

type learningsSearchInput struct {
	Query   string `json:"query,omitempty"`
	Limit   int    `json:"limit,omitempty"`
	ModelID string `json:"model_id,omitempty"` // pass "*" to disable model filter
}

func (t *LearningsSearchTool) Execute(_ context.Context, in json.RawMessage) (json.RawMessage, error) {
	if t.q == nil {
		return errorJSON("learnings store not configured"), nil
	}
	var req learningsSearchInput
	if len(in) > 0 {
		if err := json.Unmarshal(in, &req); err != nil {
			return nil, fmt.Errorf("learnings_search: %w", err)
		}
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 20
	}

	modelFilter := req.ModelID
	if modelFilter == "*" {
		modelFilter = ""
	} else if modelFilter == "" && t.m != nil {
		modelFilter = t.m.Model()
	}

	var (
		hits []plan.Learning
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
	// newest-first
	for i, j := 0, len(hits)-1; i < j; i, j = i+1, j-1 {
		hits[i], hits[j] = hits[j], hits[i]
	}
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return json.Marshal(map[string]any{
		"hits":  hits,
		"count": len(hits),
		"query": req.Query,
	})
}

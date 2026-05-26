// Package tools — segment_create / segment_list / segment_rank /
// segment_load / segment_delete expose the memory.SegmentStore
// to the agent (§8.2 Phase 5 #3 MemAgent segment memory).
//
// Use case: in a massive codebase, the agent uses these tools to
// define labeled slices ("auth module", "payment tests") and
// retrieve top-K relevant slices for a given query — replacing
// "grep the whole repo" with "load the auth segment".
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Sahaj-Tech-ltd/overkill/internal/memory"
)

// SegmentsStore is the minimal surface the tools need.
type SegmentsStore interface {
	Create(seg *memory.Segment) (*memory.Segment, error)
	Get(id string) (*memory.Segment, error)
	Delete(id string) error
	All() ([]*memory.Segment, error)
	Search(query string) ([]*memory.Segment, error)
	Rank(query string, topK int, opts memory.RankOptions) ([]memory.SegmentHit, error)
	Touch(id string) error
	LoadFiles(id string) ([]string, error)
}

// ── segment_create ──────────────────────────────────────────────────

type SegmentCreateTool struct{ store SegmentsStore }

func NewSegmentCreateTool(s SegmentsStore) *SegmentCreateTool { return &SegmentCreateTool{store: s} }
func (t *SegmentCreateTool) Name() string                     { return "segment_create" }

type segmentCreateInput struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Globs       []string `json:"globs"`
	RootDir     string   `json:"root_dir,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

func (t *SegmentCreateTool) Execute(_ context.Context, in json.RawMessage) (json.RawMessage, error) {
	if t.store == nil {
		return errorJSON("segments store not configured"), nil
	}
	var req segmentCreateInput
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, fmt.Errorf("segment_create: %w", err)
	}
	if strings.TrimSpace(req.Name) == "" || len(req.Globs) == 0 {
		return errorJSON("segment_create: 'name' and at least one glob are required"), nil
	}
	seg, err := t.store.Create(&memory.Segment{
		Name:        req.Name,
		Description: req.Description,
		Globs:       req.Globs,
		RootDir:     req.RootDir,
		Tags:        req.Tags,
	})
	if err != nil {
		return errorJSON(err.Error()), nil
	}
	return json.Marshal(seg)
}

// ── segment_list ────────────────────────────────────────────────────

type SegmentListTool struct{ store SegmentsStore }

func NewSegmentListTool(s SegmentsStore) *SegmentListTool { return &SegmentListTool{store: s} }
func (t *SegmentListTool) Name() string                   { return "segment_list" }

type segmentListInput struct {
	Query string `json:"query,omitempty"`
}

func (t *SegmentListTool) Execute(_ context.Context, in json.RawMessage) (json.RawMessage, error) {
	if t.store == nil {
		return errorJSON("segments store not configured"), nil
	}
	var req segmentListInput
	if len(in) > 0 {
		_ = json.Unmarshal(in, &req)
	}
	var (
		out []*memory.Segment
		err error
	)
	if req.Query == "" {
		out, err = t.store.All()
	} else {
		out, err = t.store.Search(req.Query)
	}
	if err != nil {
		return errorJSON(err.Error()), nil
	}
	return json.Marshal(map[string]any{"segments": out, "count": len(out)})
}

// ── segment_rank ────────────────────────────────────────────────────

type SegmentRankTool struct{ store SegmentsStore }

func NewSegmentRankTool(s SegmentsStore) *SegmentRankTool { return &SegmentRankTool{store: s} }
func (t *SegmentRankTool) Name() string                   { return "segment_rank" }

type segmentRankInput struct {
	Query string `json:"query,omitempty"`
	TopK  int    `json:"top_k,omitempty"`
}

func (t *SegmentRankTool) Execute(_ context.Context, in json.RawMessage) (json.RawMessage, error) {
	if t.store == nil {
		return errorJSON("segments store not configured"), nil
	}
	var req segmentRankInput
	if len(in) > 0 {
		_ = json.Unmarshal(in, &req)
	}
	if req.TopK <= 0 {
		req.TopK = 5
	}
	hits, err := t.store.Rank(req.Query, req.TopK, memory.RankOptions{})
	if err != nil {
		return errorJSON(err.Error()), nil
	}
	return json.Marshal(map[string]any{"hits": hits, "count": len(hits)})
}

// ── segment_load ────────────────────────────────────────────────────

type SegmentLoadTool struct{ store SegmentsStore }

func NewSegmentLoadTool(s SegmentsStore) *SegmentLoadTool { return &SegmentLoadTool{store: s} }
func (t *SegmentLoadTool) Name() string                   { return "segment_load" }

type segmentLoadInput struct {
	ID string `json:"id"`
}

func (t *SegmentLoadTool) Execute(_ context.Context, in json.RawMessage) (json.RawMessage, error) {
	if t.store == nil {
		return errorJSON("segments store not configured"), nil
	}
	var req segmentLoadInput
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, fmt.Errorf("segment_load: %w", err)
	}
	if req.ID == "" {
		return errorJSON("segment_load: 'id' is required"), nil
	}
	files, err := t.store.LoadFiles(req.ID)
	if err != nil {
		return errorJSON(err.Error()), nil
	}
	// Bump access count so recency scoring tracks real use.
	_ = t.store.Touch(req.ID)
	return json.Marshal(map[string]any{
		"id":    req.ID,
		"files": files,
		"count": len(files),
	})
}

// ── segment_delete ──────────────────────────────────────────────────

type SegmentDeleteTool struct{ store SegmentsStore }

func NewSegmentDeleteTool(s SegmentsStore) *SegmentDeleteTool { return &SegmentDeleteTool{store: s} }
func (t *SegmentDeleteTool) Name() string                     { return "segment_delete" }

type segmentDeleteInput struct {
	ID string `json:"id"`
}

func (t *SegmentDeleteTool) Execute(_ context.Context, in json.RawMessage) (json.RawMessage, error) {
	if t.store == nil {
		return errorJSON("segments store not configured"), nil
	}
	var req segmentDeleteInput
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, fmt.Errorf("segment_delete: %w", err)
	}
	if req.ID == "" {
		return errorJSON("segment_delete: 'id' is required"), nil
	}
	if err := t.store.Delete(req.ID); err != nil {
		return errorJSON(err.Error()), nil
	}
	return json.Marshal(map[string]any{"ok": true, "id": req.ID})
}

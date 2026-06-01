// Package tools — journal_search / journal_timeline / journal_get
// surface the FlightRecorder 3-layer query protocol to the agent
// (master plan §4.19).
//
// Layered disclosure: cheap index first (search), zoom into surrounding
// context (timeline), pull full detail only when needed (get). Designed
// to keep the agent's hot path budget-conscious — many results at low
// cost, drill in selectively.
package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Sahaj-Tech-ltd/overkill/internal/journal"
)

// JournalQuerier is the minimal surface the journal tools need from a
// real *journal.FlightRecorder. Defined locally so internal/tools
// doesn't take a hard dep beyond the entry types it serialises.
type JournalQuerier interface {
	SearchFlight(opts journal.FlightSearchOptions) ([]journal.FlightIndexHit, error)
	TimelineFlight(anchorID string, depth int) ([]journal.Entry, error)
	GetFlight(id string) (*journal.Entry, error)
}

// ---- search ----

type JournalSearchTool struct {
	q JournalQuerier
}

func NewJournalSearchTool(q JournalQuerier) *JournalSearchTool {
	return &JournalSearchTool{q: q}
}

func (t *JournalSearchTool) Name() string { return "journal_search" }

type journalSearchInput struct {
	Query   string `json:"query"`
	Type    string `json:"type,omitempty"`    // user_input | agent_reply | tool_call | tool_result | error | system
	Session string `json:"session,omitempty"` // session ID; empty = any
	Limit   int    `json:"limit,omitempty"`   // default 20
}

func (t *JournalSearchTool) Execute(_ context.Context, in json.RawMessage) (json.RawMessage, error) {
	if t.q == nil {
		return errorJSON("journal querier not configured"), nil
	}
	var req journalSearchInput
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, fmt.Errorf("journal_search: %w", err)
	}
	hits, err := t.q.SearchFlight(journal.FlightSearchOptions{
		Query:   req.Query,
		Type:    journal.EntryType(req.Type),
		Session: req.Session,
		Limit:   req.Limit,
	})
	if err != nil {
		return errorJSON(err.Error()), nil
	}
	body, _ := json.Marshal(map[string]any{
		"hits":  hits,
		"count": len(hits),
	})
	return body, nil
}

// ---- timeline ----

type JournalTimelineTool struct {
	q JournalQuerier
}

func NewJournalTimelineTool(q JournalQuerier) *JournalTimelineTool {
	return &JournalTimelineTool{q: q}
}

func (t *JournalTimelineTool) Name() string { return "journal_timeline" }

type journalTimelineInput struct {
	AnchorID string `json:"anchor_id"`
	Depth    int    `json:"depth,omitempty"` // default 5
}

func (t *JournalTimelineTool) Execute(_ context.Context, in json.RawMessage) (json.RawMessage, error) {
	if t.q == nil {
		return errorJSON("journal querier not configured"), nil
	}
	var req journalTimelineInput
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, fmt.Errorf("journal_timeline: %w", err)
	}
	if req.AnchorID == "" {
		return errorJSON("anchor_id is required"), nil
	}
	entries, err := t.q.TimelineFlight(req.AnchorID, req.Depth)
	if err != nil {
		return errorJSON(err.Error()), nil
	}
	body, _ := json.Marshal(map[string]any{
		"entries": entries,
		"count":   len(entries),
	})
	return body, nil
}

// ---- get ----

type JournalGetTool struct {
	q JournalQuerier
}

func NewJournalGetTool(q JournalQuerier) *JournalGetTool {
	return &JournalGetTool{q: q}
}

func (t *JournalGetTool) Name() string { return "journal_get" }

type journalGetInput struct {
	ID string `json:"id"`
}

func (t *JournalGetTool) Execute(_ context.Context, in json.RawMessage) (json.RawMessage, error) {
	if t.q == nil {
		return errorJSON("journal querier not configured"), nil
	}
	var req journalGetInput
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, fmt.Errorf("journal_get: %w", err)
	}
	if req.ID == "" {
		return errorJSON("id is required"), nil
	}
	entry, err := t.q.GetFlight(req.ID)
	if err != nil {
		return errorJSON(err.Error()), nil
	}
	if entry == nil {
		return errorJSON(fmt.Sprintf("entry %s not found", req.ID)), nil
	}
	body, _ := json.Marshal(entry)
	return body, nil
}

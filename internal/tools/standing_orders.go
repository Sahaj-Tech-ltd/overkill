// Package tools — standing_order_add / remove / toggle / list lets
// the agent mutate its own standing orders during a session
// (§7.1 Layer 5 self-update).
//
// Why typed tools instead of direct file writes: the standing-
// orders JSONL is a small high-leverage file. A hallucinating turn
// rewriting it via Write could replace "never auto-commit" with
// "always auto-commit", and the next session would silently honor
// the corruption. The protected-path gate refuses raw writes to
// the standing-orders directory; mutations come through here, with
// validation (non-empty text, valid IDs) and structured persistence.
//
// EVR (Execute-Verify-Report) shape: the optional `verify` + `report`
// inputs let the agent promote a directive with its own follow-up
// procedure. Plain `text` is the common case.

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Sahaj-Tech-ltd/overkill/internal/automation"
)

// StandingOrdersStore is the minimal surface the tools need.
type StandingOrdersStore interface {
	AddEVR(text, verify, report string) (*automation.StandingOrder, error)
	Remove(id string) error
	SetEnabled(id string, enabled bool) error
	All() []automation.StandingOrder
}

// ── standing_order_add ──────────────────────────────────────────────

type StandingOrderAddTool struct{ store StandingOrdersStore }

func NewStandingOrderAddTool(s StandingOrdersStore) *StandingOrderAddTool {
	return &StandingOrderAddTool{store: s}
}
func (t *StandingOrderAddTool) Name() string { return "standing_order_add" }

type standingOrderAddInput struct {
	Text   string `json:"text"`
	Verify string `json:"verify,omitempty"`
	Report string `json:"report,omitempty"`
}

func (t *StandingOrderAddTool) Execute(_ context.Context, in json.RawMessage) (json.RawMessage, error) {
	if t.store == nil {
		return errorJSON("standing-orders store not configured"), nil
	}
	var req standingOrderAddInput
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, fmt.Errorf("standing_order_add: %w", err)
	}
	if strings.TrimSpace(req.Text) == "" {
		return errorJSON("standing_order_add: 'text' is required"), nil
	}
	so, err := t.store.AddEVR(req.Text, req.Verify, req.Report)
	if err != nil {
		return errorJSON(err.Error()), nil
	}
	return json.Marshal(so)
}

// ── standing_order_remove ───────────────────────────────────────────

type StandingOrderRemoveTool struct{ store StandingOrdersStore }

func NewStandingOrderRemoveTool(s StandingOrdersStore) *StandingOrderRemoveTool {
	return &StandingOrderRemoveTool{store: s}
}
func (t *StandingOrderRemoveTool) Name() string { return "standing_order_remove" }

type standingOrderRemoveInput struct {
	ID string `json:"id"`
}

func (t *StandingOrderRemoveTool) Execute(_ context.Context, in json.RawMessage) (json.RawMessage, error) {
	if t.store == nil {
		return errorJSON("standing-orders store not configured"), nil
	}
	var req standingOrderRemoveInput
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, fmt.Errorf("standing_order_remove: %w", err)
	}
	if req.ID == "" {
		return errorJSON("standing_order_remove: 'id' is required"), nil
	}
	if err := t.store.Remove(req.ID); err != nil {
		return errorJSON(err.Error()), nil
	}
	return json.Marshal(map[string]any{"ok": true, "id": req.ID})
}

// ── standing_order_toggle ───────────────────────────────────────────

type StandingOrderToggleTool struct{ store StandingOrdersStore }

func NewStandingOrderToggleTool(s StandingOrdersStore) *StandingOrderToggleTool {
	return &StandingOrderToggleTool{store: s}
}
func (t *StandingOrderToggleTool) Name() string { return "standing_order_toggle" }

type standingOrderToggleInput struct {
	ID      string `json:"id"`
	Enabled bool   `json:"enabled"`
}

func (t *StandingOrderToggleTool) Execute(_ context.Context, in json.RawMessage) (json.RawMessage, error) {
	if t.store == nil {
		return errorJSON("standing-orders store not configured"), nil
	}
	var req standingOrderToggleInput
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, fmt.Errorf("standing_order_toggle: %w", err)
	}
	if req.ID == "" {
		return errorJSON("standing_order_toggle: 'id' is required"), nil
	}
	if err := t.store.SetEnabled(req.ID, req.Enabled); err != nil {
		return errorJSON(err.Error()), nil
	}
	return json.Marshal(map[string]any{"ok": true, "id": req.ID, "enabled": req.Enabled})
}

// ── standing_order_list ─────────────────────────────────────────────

type StandingOrderListTool struct{ store StandingOrdersStore }

func NewStandingOrderListTool(s StandingOrdersStore) *StandingOrderListTool {
	return &StandingOrderListTool{store: s}
}
func (t *StandingOrderListTool) Name() string { return "standing_order_list" }

func (t *StandingOrderListTool) Execute(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
	if t.store == nil {
		return errorJSON("standing-orders store not configured"), nil
	}
	return json.Marshal(map[string]any{"orders": t.store.All()})
}

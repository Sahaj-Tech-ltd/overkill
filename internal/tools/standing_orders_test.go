package tools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/automation"
)

func newOrdersStore(t *testing.T) *automation.OrdersFile {
	t.Helper()
	store, err := automation.NewOrdersFile(filepath.Join(t.TempDir(), "orders.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestStandingOrderAdd_PersistsViaEVR(t *testing.T) {
	store := newOrdersStore(t)
	tool := NewStandingOrderAddTool(store)
	in, _ := json.Marshal(map[string]any{
		"text":   "always run go vet after editing .go files",
		"verify": "go vet ./...",
		"report": "vet output is clean",
	})
	out, err := tool.Execute(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	var so automation.StandingOrder
	if err := json.Unmarshal(out, &so); err != nil {
		t.Fatal(err)
	}
	if so.Text == "" || so.Verify == "" || so.Report == "" {
		t.Errorf("EVR fields should be populated: %+v", so)
	}
	all := store.All()
	if len(all) != 1 {
		t.Errorf("expected 1 stored order, got %d", len(all))
	}
}

func TestStandingOrderAdd_RejectsEmptyText(t *testing.T) {
	tool := NewStandingOrderAddTool(newOrdersStore(t))
	out, _ := tool.Execute(context.Background(), json.RawMessage(`{}`))
	var resp map[string]any
	_ = json.Unmarshal(out, &resp)
	if resp["error"] == nil {
		t.Errorf("empty text should error: %s", string(out))
	}
}

func TestStandingOrderRemove_DeletesByID(t *testing.T) {
	store := newOrdersStore(t)
	so, _ := store.Add("rule 1")
	tool := NewStandingOrderRemoveTool(store)
	in, _ := json.Marshal(map[string]any{"id": so.ID})
	if _, err := tool.Execute(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if len(store.All()) != 0 {
		t.Errorf("expected store empty after remove")
	}
}

func TestStandingOrderToggle_FlipsEnable(t *testing.T) {
	store := newOrdersStore(t)
	so, _ := store.Add("rule")
	tool := NewStandingOrderToggleTool(store)
	in, _ := json.Marshal(map[string]any{"id": so.ID, "enabled": false})
	if _, err := tool.Execute(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if store.All()[0].Enabled {
		t.Error("toggle did not disable")
	}
}

func TestStandingOrderList_ReturnsAll(t *testing.T) {
	store := newOrdersStore(t)
	_, _ = store.Add("a")
	_, _ = store.Add("b")
	tool := NewStandingOrderListTool(store)
	out, _ := tool.Execute(context.Background(), nil)
	var resp struct {
		Orders []automation.StandingOrder `json:"orders"`
	}
	_ = json.Unmarshal(out, &resp)
	if len(resp.Orders) != 2 {
		t.Errorf("expected 2 orders, got %d", len(resp.Orders))
	}
}

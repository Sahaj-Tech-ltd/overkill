package automation

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestStandingOrders_AddListPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orders.jsonl")
	o, err := NewOrdersFile(path)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	so, err := o.Add("always run gofmt before commit")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if so.ID == "" || !so.Enabled {
		t.Fatalf("bad order: %+v", so)
	}

	o2, err := NewOrdersFile(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	all := o2.All()
	if len(all) != 1 || all[0].Text != "always run gofmt before commit" {
		t.Fatalf("did not persist: %+v", all)
	}
}

func TestStandingOrders_RemoveAndToggle(t *testing.T) {
	o, _ := NewOrdersFile(filepath.Join(t.TempDir(), "x.jsonl"))
	a, _ := o.Add("first")
	b, _ := o.Add("second")

	if err := o.SetEnabled(a.ID, false); err != nil {
		t.Fatal(err)
	}
	active := o.Active()
	if len(active) != 1 || active[0].ID != b.ID {
		t.Fatalf("expected only b active, got %+v", active)
	}

	if err := o.Remove(b.ID); err != nil {
		t.Fatal(err)
	}
	if len(o.All()) != 1 {
		t.Fatalf("expected 1 left, got %d", len(o.All()))
	}
}

func TestStandingOrders_RemoveUnknown(t *testing.T) {
	o, _ := NewOrdersFile(filepath.Join(t.TempDir(), "x.jsonl"))
	if err := o.Remove("nope"); err == nil {
		t.Fatal("expected error")
	}
}

func TestStandingOrders_PromptSnippet(t *testing.T) {
	o, _ := NewOrdersFile(filepath.Join(t.TempDir(), "x.jsonl"))
	if got := o.PromptSnippet(); got != "" {
		t.Fatalf("empty -> empty snippet, got %q", got)
	}
	o.Add("rule one")
	o.Add("rule two")
	snip := o.PromptSnippet()
	if !strings.Contains(snip, "rule one") || !strings.Contains(snip, "rule two") {
		t.Fatalf("snippet missing entries: %q", snip)
	}
	if !strings.Contains(snip, "STANDING ORDERS") {
		t.Fatalf("missing header: %q", snip)
	}
}

func TestStandingOrders_AddRequiresText(t *testing.T) {
	o, _ := NewOrdersFile(filepath.Join(t.TempDir(), "x.jsonl"))
	if _, err := o.Add("   "); err == nil {
		t.Fatal("expected error on whitespace-only")
	}
}

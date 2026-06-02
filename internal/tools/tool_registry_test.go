package tools

import (
	"context"
	"encoding/json"
	"testing"
)

type stubTool struct{ name string }

func (s *stubTool) Name() string { return s.name }
func (s *stubTool) Execute(ctx context.Context, in json.RawMessage) (json.RawMessage, error) {
	return nil, nil
}

func TestRegistry_HasReportsRegisteredTool(t *testing.T) {
	r := NewRegistry()
	if r.Has("nope") {
		t.Fatal("expected Has=false for unregistered tool")
	}
	if err := r.Register(&stubTool{name: "x"}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if !r.Has("x") {
		t.Fatal("expected Has=true after register")
	}
}

func TestRegistry_HasNilReceiverSafe(t *testing.T) {
	var r *Registry
	if r.Has("x") {
		t.Fatal("expected nil receiver Has to return false")
	}
}

func TestRegistry_RegisterDuplicateErrors(t *testing.T) {
	r := NewRegistry()
	_ = r.Register(&stubTool{name: "x"})
	if err := r.Register(&stubTool{name: "x"}); err == nil {
		t.Fatal("expected duplicate-register error")
	}
}

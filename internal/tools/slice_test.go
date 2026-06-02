package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestSliceDecomposeTool_MissingSpec(t *testing.T) {
	tool := NewSliceDecomposeTool()
	got, _ := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if !strings.Contains(string(got), "spec is required") {
		t.Errorf("expected spec-required error, got %s", got)
	}
}

func TestSliceDecomposeTool_BadJSON(t *testing.T) {
	tool := NewSliceDecomposeTool()
	_, err := tool.Execute(context.Background(), json.RawMessage(`not-json`))
	if err == nil {
		t.Error("expected unmarshal error for bad input")
	}
}

func TestSliceDecomposeTool_HappyPath(t *testing.T) {
	tool := NewSliceDecomposeTool()
	got, err := tool.Execute(context.Background(),
		json.RawMessage(`{"spec":"add an /auth endpoint and a login UI"}`))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var out struct {
		Count  int              `json:"count"`
		Slices []map[string]any `json:"slices"`
	}
	if err := json.Unmarshal(got, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Count < 1 {
		t.Errorf("expected at least 1 slice, got %d", out.Count)
	}
	if len(out.Slices) != out.Count {
		t.Errorf("count/slices mismatch: %d vs %d", out.Count, len(out.Slices))
	}
}

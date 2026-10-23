package subagent

import (
	"context"
	"encoding/json"
	"testing"
)

func TestCanvasEffectsCannotForgeFilesystemScope(t *testing.T) {
	for _, input := range []string{`{"action":"reset"}`, `{"action":"apply","jsonl":"{}","path":"src/allowed.go"}`} {
		inner := &fakeTool{name: "canvas"}
		wrapped := NewEnforcedTool(inner, &Contract{Scope: []string{"src"}}, NewMemAuditWriter(), nil)
		_, err := wrapped.Execute(context.Background(), json.RawMessage(input))
		if err == nil || inner.called {
			t.Fatalf("scoped canvas effect accepted %s", input)
		}
	}
	inner := &fakeTool{name: "canvas", out: json.RawMessage(`{}`)}
	wrapped := NewEnforcedTool(inner, &Contract{Scope: []string{"src"}}, NewMemAuditWriter(), nil)
	if _, err := wrapped.Execute(context.Background(), json.RawMessage(`{"action":"state"}`)); err != nil || !inner.called {
		t.Fatalf("state read blocked: %v", err)
	}
}

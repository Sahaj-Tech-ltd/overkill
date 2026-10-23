package security

import (
	"encoding/json"
	"testing"
)

func TestCanvasReaderAllowsOnlyState(t *testing.T) {
	gate := NewPrivilegeGate(ModeReader)
	for _, input := range []string{`{"action":"apply","jsonl":"{}"}`, `{"action":"reset"}`, `{}`, `null`, `garbage`} {
		allowed, _ := gate.Allow("canvas", json.RawMessage(input))
		if allowed {
			t.Fatalf("reader accepted %s", input)
		}
	}
	allowed, _ := gate.Allow("canvas", json.RawMessage(`{"action":"state"}`))
	if !allowed {
		t.Fatal("reader must allow canvas state")
	}
}

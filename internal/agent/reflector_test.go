package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
)

// stubReflector records every Reflect call and returns canned notes
// so we can assert what landed in history without depending on the
// real classifier.
type stubReflector struct {
	failures []Failure
	notes    []string
	isFail   func(toolName, output, errStr string) bool
}

func (s *stubReflector) IsFailure(toolName, output, errStr string) bool {
	if s.isFail != nil {
		return s.isFail(toolName, output, errStr)
	}
	return errStr != "" || strings.Contains(output, "FAIL")
}

func (s *stubReflector) Reflect(f Failure) Reflection {
	s.failures = append(s.failures, f)
	return Reflection{
		Mode:       "test_failure",
		RootCause:  "stub: " + f.ToolName + " failed",
		Hypothesis: "stub: try something else",
		Confidence: 0.7,
	}
}

func (s *stubReflector) FormatNote(toolName string, r Reflection) string {
	note := fmt.Sprintf("[stub-reflexion] %s: %s -> %s", toolName, r.RootCause, r.Hypothesis)
	s.notes = append(s.notes, note)
	return note
}

func TestAgent_Reflector_InjectsNoteOnToolError(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(&mockTool{
		name: "broken",
		execute: func(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
			return nil, fmt.Errorf("disk full")
		},
	})
	p := &mockProvider{
		responses: []providers.Response{
			{ToolCalls: []providers.ToolCall{{ID: "tc1", Name: "broken", Arguments: `{}`}}},
			{Content: "done"},
		},
	}
	a := newTestAgent(p, reg, nil, nil)
	rf := &stubReflector{}
	a.SetReflector(rf)

	if _, err := a.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run error: %v", err)
	}

	if len(rf.failures) != 1 {
		t.Fatalf("expected 1 reflect call, got %d", len(rf.failures))
	}
	if !strings.Contains(rf.failures[0].Error, "disk full") {
		t.Errorf("Failure.Error not propagated: %q", rf.failures[0].Error)
	}

	systemNoteFound := false
	for _, m := range a.History() {
		if m.Role == "system" && strings.Contains(m.Content, "[stub-reflexion]") {
			systemNoteFound = true
		}
	}
	if !systemNoteFound {
		t.Errorf("reflexion system note missing from history: %+v", a.History())
	}
}

func TestAgent_Reflector_DoesNotFireOnSuccess(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(&mockTool{
		name: "ok_tool",
		execute: func(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`{"ok": true}`), nil
		},
	})
	p := &mockProvider{
		responses: []providers.Response{
			{ToolCalls: []providers.ToolCall{{ID: "tc1", Name: "ok_tool", Arguments: `{}`}}},
			{Content: "done"},
		},
	}
	a := newTestAgent(p, reg, nil, nil)
	rf := &stubReflector{}
	a.SetReflector(rf)

	if _, err := a.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if len(rf.failures) != 0 {
		t.Errorf("reflector should not fire on success, got %d calls", len(rf.failures))
	}
}

func TestAgent_Reflector_BudgetCapsNotesPerTurn(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(&mockTool{
		name: "broken",
		execute: func(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
			return nil, fmt.Errorf("disk full")
		},
	})
	// One assistant message with THREE parallel failing tool calls.
	p := &mockProvider{
		responses: []providers.Response{
			{ToolCalls: []providers.ToolCall{
				{ID: "tc1", Name: "broken", Arguments: `{}`},
				{ID: "tc2", Name: "broken", Arguments: `{}`},
				{ID: "tc3", Name: "broken", Arguments: `{}`},
			}},
			{Content: "done"},
		},
	}
	a := newTestAgent(p, reg, nil, nil)
	rf := &stubReflector{}
	a.SetReflector(rf)
	a.SetReflectionBudget(2)

	if _, err := a.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if len(rf.failures) != 2 {
		t.Errorf("expected reflection budget=2 to cap calls, got %d", len(rf.failures))
	}
}

func TestAgent_Reflector_DisabledWhenBudgetZero(t *testing.T) {
	reg := tools.NewRegistry()
	reg.Register(&mockTool{
		name: "broken",
		execute: func(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
			return nil, fmt.Errorf("nope")
		},
	})
	p := &mockProvider{
		responses: []providers.Response{
			{ToolCalls: []providers.ToolCall{{ID: "tc1", Name: "broken", Arguments: `{}`}}},
			{Content: "done"},
		},
	}
	a := newTestAgent(p, reg, nil, nil)
	rf := &stubReflector{}
	a.SetReflector(rf)
	a.SetReflectionBudget(0) // disable

	if _, err := a.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if len(rf.failures) != 0 {
		t.Errorf("budget=0 should disable, got %d reflect calls", len(rf.failures))
	}
}

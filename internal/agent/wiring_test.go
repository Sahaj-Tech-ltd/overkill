package agent

// Integration tests verifying that previously-orphaned subsystems are now
// reached from the agent loop. Each test owns its own mock provider/tool so
// failures localize to the wiring under test, not the broader agent harness.

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
)

// TestWiring_ForethinkerEmitsImpact ensures the Forethinker is invoked before
// each tool call and publishes a tool_impact event.
func TestWiring_ForethinkerEmitsImpact(t *testing.T) {
	reg := tools.NewRegistry()
	_ = reg.Register(&mockTool{
		name: "shell",
		execute: func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`{"stdout":"ok"}`), nil
		},
	})
	p := &mockProvider{
		responses: []providers.Response{
			{
				Content: "running",
				ToolCalls: []providers.ToolCall{
					{ID: "1", Name: "shell", Arguments: `{"command":"rm /tmp/foo"}`},
				},
			},
			{Content: "done"},
		},
	}
	a := newTestAgent(p, reg, nil, nil)

	var got atomic.Int32
	var mu sync.Mutex
	var lastEvent string
	a.SetEventFn(func(event string, payload map[string]any) {
		if event == "tool_impact" {
			got.Add(1)
			mu.Lock()
			lastEvent = event
			mu.Unlock()
		}
	})

	if _, err := a.Run(context.Background(), "delete that file"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.Load() == 0 {
		t.Fatalf("expected tool_impact event, got none")
	}
	mu.Lock()
	if lastEvent != "tool_impact" {
		t.Fatalf("unexpected event %q", lastEvent)
	}
	mu.Unlock()
}

// TestWiring_ConfidenceAttachedToResult verifies AssessConfidence is called at
// the end of Run and attached to RunResult.
func TestWiring_ConfidenceAttachedToResult(t *testing.T) {
	p := &mockProvider{
		responses: []providers.Response{{Content: "hi", Usage: providers.Usage{InputTokens: 1, OutputTokens: 1}}},
	}
	a := newTestAgent(p, nil, nil, nil)
	res, err := a.Run(context.Background(), "explain this")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Confidence == nil {
		t.Fatal("expected Confidence assessment, got nil")
	}
	if res.Confidence.TaskType == "" {
		t.Fatal("expected non-empty TaskType")
	}
}

// TestWiring_RecoveryEmittedOnError verifies emitRecovery fires when step()
// fails, and that the agent's bus + callback both see it.
func TestWiring_RecoveryEmittedOnError(t *testing.T) {
	a := New(Config{
		Provider:     &errProvider{err: errors.New("boom")},
		Tools:        tools.NewRegistry(),
		Tokenizer:    nil,
		Model:        "test-model",
		MaxTokens:    4096,
		SystemPrompt: "x",
		MaxSteps:     2,
		SessionID:    "test",
	})

	var seen atomic.Int32
	a.SetEventFn(func(event string, payload map[string]any) {
		if event == "recovery" {
			seen.Add(1)
		}
	})

	_, err := a.Run(context.Background(), "do something")
	if err == nil {
		t.Fatal("expected error from failing provider")
	}
	if seen.Load() == 0 {
		t.Fatal("expected recovery event after step error")
	}
}

// TestWiring_BudgetWarningEmits verifies the budget estimator emits a warning
// event when the soft threshold is crossed.
func TestWiring_BudgetWarningEmits(t *testing.T) {
	p := &mockProvider{
		responses: []providers.Response{{Content: "ok"}},
	}
	a := newTestAgent(p, nil, nil, nil)
	// Force the budget to look saturated by giving the agent a small max.
	// Small enough to trigger soft warning (80%) but not hard exceed (95%).
	// System prompt is ~1178 tokens, so 1400 → ~84% utilization.
	a.maxTokens = 1400
	a.budgetEstimator = NewBudgetEstimator(a.tokenizer, 1400)

	var seen atomic.Int32
	a.SetEventFn(func(event string, payload map[string]any) {
		if event == "budget_warning" {
			seen.Add(1)
		}
	})

	if _, err := a.Run(context.Background(), "hi"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if seen.Load() == 0 {
		t.Fatal("expected budget_warning event")
	}
}

// TestWiring_BusReceivesEvents verifies the internal EventBus mirrors emit()
// events so subscribers other than the host can listen.
func TestWiring_BusReceivesEvents(t *testing.T) {
	p := &mockProvider{
		responses: []providers.Response{{Content: "hello"}},
	}
	a := newTestAgent(p, nil, nil, nil)

	bus := a.Bus()
	if bus == nil {
		t.Fatal("Bus() returned nil")
	}
	sub := bus.Subscribe("custom", 4)
	defer bus.Unsubscribe(sub)

	a.emit("custom", map[string]any{"hello": "world"})

	select {
	case ev := <-sub.Ch:
		if ev.Kind != "custom" {
			t.Fatalf("unexpected kind %q", ev.Kind)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for custom event on bus")
	}
}

// errProvider always returns the configured error from Complete and Stream.
type errProvider struct{ err error }

func (e *errProvider) Complete(_ context.Context, _ providers.Request) (providers.Response, error) {
	return providers.Response{}, e.err
}
func (e *errProvider) Stream(_ context.Context, _ providers.Request) (<-chan providers.Chunk, error) {
	return nil, e.err
}
func (e *errProvider) Models() []providers.Model { return nil }
func (e *errProvider) Name() string              { return "errprovider" }

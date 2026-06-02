package agent

import (
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

func TestDrainSteering_NoQueueIsNoOp(t *testing.T) {
	a := &Agent{}
	if got := a.drainSteering(); got {
		t.Error("no queue should drain false")
	}
}

func TestDrainSteering_EmptyQueueIsNoOp(t *testing.T) {
	a := &Agent{steering: NewSteeringQueue(SteeringDrainAll)}
	if got := a.drainSteering(); got {
		t.Error("empty queue should drain false")
	}
}

func TestDrainSteering_AppendsToHistory(t *testing.T) {
	a := &Agent{
		steering: NewSteeringQueue(SteeringDrainAll),
		history:  []providers.Message{},
	}
	a.steering.Inject(SteeredMessage{Content: "actually use bcrypt", Role: "system"})
	a.steering.Inject(SteeredMessage{Content: "and add a timing-safe compare", Role: "system"})

	if got := a.drainSteering(); !got {
		t.Fatal("drained=true expected")
	}
	if len(a.history) != 2 {
		t.Fatalf("expected 2 messages in history, got %d", len(a.history))
	}
	if a.history[0].Content != "actually use bcrypt" {
		t.Errorf("first msg content = %q", a.history[0].Content)
	}
	if a.history[0].Role != "system" {
		t.Errorf("expected system role, got %q", a.history[0].Role)
	}
}

func TestDrainSteering_OneAtATime(t *testing.T) {
	a := &Agent{
		steering: NewSteeringQueue(SteeringOneAtATime),
		history:  []providers.Message{},
	}
	a.steering.Inject(SteeredMessage{Content: "first"})
	a.steering.Inject(SteeredMessage{Content: "second"})

	a.drainSteering()
	if len(a.history) != 1 {
		t.Errorf("one-at-a-time should drain 1, got %d in history", len(a.history))
	}
	if a.steering.Pending() != 1 {
		t.Errorf("one-at-a-time should leave 1 pending, got %d", a.steering.Pending())
	}
}

func TestDrainSteering_DefaultRoleIsSystem(t *testing.T) {
	a := &Agent{
		steering: NewSteeringQueue(SteeringDrainAll),
		history:  []providers.Message{},
	}
	// Role intentionally blank to test default.
	a.steering.Inject(SteeredMessage{Content: "guidance"})
	a.drainSteering()
	if a.history[0].Role != "system" {
		t.Errorf("blank role should default to system, got %q", a.history[0].Role)
	}
}

package agent

import "testing"

func TestSteeringQueue_AppendDrain(t *testing.T) {
	sq := NewSteeringQueue(SteeringDrainAll)
	sq.Append("first")
	sq.Append("second")
	sq.Append("third")

	// Drain one at a time, FIFO order
	if got := sq.Drain(); got != "first" {
		t.Fatalf("first drain: expected 'first', got %q", got)
	}
	if got := sq.Drain(); got != "second" {
		t.Fatalf("second drain: expected 'second', got %q", got)
	}
	if got := sq.Drain(); got != "third" {
		t.Fatalf("third drain: expected 'third', got %q", got)
	}
	// Queue should now be empty
	if sq.Pending() != 0 {
		t.Fatalf("expected 0 pending, got %d", sq.Pending())
	}
}

func TestSteeringQueue_DrainEmpty(t *testing.T) {
	sq := NewSteeringQueue(SteeringDrainAll)

	if got := sq.Drain(); got != "" {
		t.Fatalf("drain on empty: expected '', got %q", got)
	}
	if sq.Pending() != 0 {
		t.Fatalf("expected 0 pending, got %d", sq.Pending())
	}
}

func TestSteeringQueue_DrainAll(t *testing.T) {
	sq := NewSteeringQueue(SteeringDrainAll)
	sq.Append("one")
	sq.Append("two")
	sq.Append("three")

	got := sq.DrainAll()
	if got != "one\ntwo\nthree" {
		t.Fatalf("DrainAll: expected 'one\\ntwo\\nthree', got %q", got)
	}
	if sq.Pending() != 0 {
		t.Fatalf("expected 0 pending after DrainAll, got %d", sq.Pending())
	}
}

func TestSteeringQueue_DrainAllEmpty(t *testing.T) {
	sq := NewSteeringQueue(SteeringDrainAll)

	if got := sq.DrainAll(); got != "" {
		t.Fatalf("DrainAll on empty: expected '', got %q", got)
	}
}

func TestAgent_Steer(t *testing.T) {
	sq := NewSteeringQueue(SteeringDrainAll)
	a := &Agent{steering: sq}

	result := a.Steer("use bcrypt instead")
	if result != "steering queued: use bcrypt instead" {
		t.Fatalf("unexpected result: %q", result)
	}
	if sq.Pending() != 1 {
		t.Fatalf("expected 1 pending, got %d", sq.Pending())
	}

	// Drain and verify content
	got := sq.Drain()
	if got != "use bcrypt instead" {
		t.Fatalf("expected 'use bcrypt instead', got %q", got)
	}
}

func TestAgent_Steer_NilQueue(t *testing.T) {
	a := &Agent{} // no steering queue

	result := a.Steer("guidance")
	if result != "steering queue not available" {
		t.Fatalf("expected 'steering queue not available', got %q", result)
	}
}

func TestAgent_Steer_NilAgent(t *testing.T) {
	var a *Agent

	result := a.Steer("guidance")
	if result != "steering not available: nil agent" {
		t.Fatalf("expected 'steering not available: nil agent', got %q", result)
	}
}

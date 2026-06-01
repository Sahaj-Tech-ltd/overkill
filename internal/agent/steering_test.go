package agent

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestSteeringInjectAndDrain(t *testing.T) {
	sq := NewSteeringQueue(SteeringDrainAll)
	sq.Inject(SteeredMessage{Content: "hello", Role: "user", Priority: 0})
	msgs := sq.Drained()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Content != "hello" {
		t.Fatalf("expected content 'hello', got %q", msgs[0].Content)
	}
}

func TestSteeringDrainAllMode(t *testing.T) {
	sq := NewSteeringQueue(SteeringDrainAll)
	sq.Inject(SteeredMessage{Content: "a", Role: "user", Priority: 1})
	sq.Inject(SteeredMessage{Content: "b", Role: "user", Priority: 2})
	sq.Inject(SteeredMessage{Content: "c", Role: "user", Priority: 0})
	msgs := sq.Drained()
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}
	if msgs[0].Content != "b" || msgs[1].Content != "a" || msgs[2].Content != "c" {
		t.Fatalf("priority order wrong: %v", msgs)
	}
	if sq.Pending() != 0 {
		t.Fatalf("expected 0 pending, got %d", sq.Pending())
	}
}

func TestSteeringOneAtATimeMode(t *testing.T) {
	sq := NewSteeringQueue(SteeringOneAtATime)
	sq.Inject(SteeredMessage{Content: "first", Role: "user", Priority: 5})
	sq.Inject(SteeredMessage{Content: "second", Role: "user", Priority: 1})
	msgs := sq.Drained()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Content != "first" {
		t.Fatalf("expected 'first', got %q", msgs[0].Content)
	}
	if sq.Pending() != 1 {
		t.Fatalf("expected 1 pending, got %d", sq.Pending())
	}
	msgs2 := sq.Drained()
	if len(msgs2) != 1 || msgs2[0].Content != "second" {
		t.Fatalf("one-at-a-time second drain wrong: %v", msgs2)
	}
}

func TestSteeringPriorityOrdering(t *testing.T) {
	sq := NewSteeringQueue(SteeringDrainAll)
	sq.Inject(SteeredMessage{Content: "low", Role: "user", Priority: 1})
	sq.Inject(SteeredMessage{Content: "high", Role: "user", Priority: 100})
	sq.Inject(SteeredMessage{Content: "mid", Role: "user", Priority: 50})
	sq.Inject(SteeredMessage{Content: "same1", Role: "user", Priority: 50})
	msgs := sq.Drained()
	expected := []string{"high", "mid", "same1", "low"}
	for i, m := range msgs {
		if m.Content != expected[i] {
			t.Fatalf("position %d: expected %q, got %q", i, expected[i], m.Content)
		}
	}
}

func TestSteeringWaitBlocksThenReturns(t *testing.T) {
	sq := NewSteeringQueue(SteeringDrainAll)
	elapsed := make(chan time.Duration, 1)
	go func() {
		start := time.Now()
		err := sq.Wait(context.Background())
		if err != nil {
			t.Errorf("wait returned error: %v", err)
		}
		elapsed <- time.Since(start)
	}()
	time.Sleep(50 * time.Millisecond)
	sq.Inject(SteeredMessage{Content: "wake", Role: "user", Priority: 0})
	dur := <-elapsed
	if dur < 40*time.Millisecond {
		t.Fatalf("wait returned too fast: %v", dur)
	}
}

func TestSteeringWaitRespectsContextCancel(t *testing.T) {
	sq := NewSteeringQueue(SteeringDrainAll)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := sq.Wait(ctx)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
	elapsed := time.Since(start)
	if elapsed > 200*time.Millisecond {
		t.Fatalf("wait took too long after cancel: %v", elapsed)
	}
}

func TestSteeringClear(t *testing.T) {
	sq := NewSteeringQueue(SteeringDrainAll)
	sq.Inject(SteeredMessage{Content: "a", Role: "user", Priority: 0})
	sq.Inject(SteeredMessage{Content: "b", Role: "user", Priority: 0})
	if sq.Pending() != 2 {
		t.Fatalf("expected 2 pending, got %d", sq.Pending())
	}
	sq.Clear()
	if sq.Pending() != 0 {
		t.Fatalf("expected 0 pending after clear, got %d", sq.Pending())
	}
	msgs := sq.Drained()
	if msgs != nil {
		t.Fatalf("expected nil after clear, got %v", msgs)
	}
}

func TestSteeringConcurrentInject(t *testing.T) {
	sq := NewSteeringQueue(SteeringDrainAll)
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			sq.Inject(SteeredMessage{Content: "msg", Role: "user", Priority: n})
		}(i)
	}
	wg.Wait()
	if sq.Pending() != 100 {
		t.Fatalf("expected 100 pending, got %d", sq.Pending())
	}
	msgs := sq.Drained()
	if len(msgs) != 100 {
		t.Fatalf("expected 100 drained, got %d", len(msgs))
	}
	for i := 1; i < len(msgs); i++ {
		if msgs[i].Priority > msgs[i-1].Priority {
			t.Fatalf("not sorted by priority desc at index %d", i)
		}
	}
}

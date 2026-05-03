package agent

import (
	"sync"
	"testing"
	"time"
)

func TestEventBus_SubscribeAndReceive(t *testing.T) {
	bus := NewEventBus()
	defer bus.Close()

	sub := bus.Subscribe("tool.call", 8)
	bus.Emit("tool.call", "bash")

	select {
	case evt := <-sub.Ch:
		if evt.Kind != "tool.call" {
			t.Fatalf("expected kind tool.call, got %s", evt.Kind)
		}
		if evt.Payload.(string) != "bash" {
			t.Fatalf("expected payload bash, got %v", evt.Payload)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestEventBus_MultipleSubscribers(t *testing.T) {
	bus := NewEventBus()
	defer bus.Close()

	sub1 := bus.Subscribe("log", 4)
	sub2 := bus.Subscribe("log", 4)

	bus.Emit("log", "hello")

	for i, sub := range []*Subscriber{sub1, sub2} {
		select {
		case evt := <-sub.Ch:
			if evt.Payload.(string) != "hello" {
				t.Fatalf("subscriber %d: expected hello, got %v", i, evt.Payload)
			}
		case <-time.After(time.Second):
			t.Fatalf("subscriber %d: timed out", i)
		}
	}
}

func TestEventBus_WildcardSubscriber(t *testing.T) {
	bus := NewEventBus()
	defer bus.Close()

	wild := bus.Subscribe("", 8)
	sub := bus.Subscribe("alpha", 4)

	bus.Emit("alpha", 1)
	bus.Emit("beta", 2)
	bus.Emit("gamma", 3)

	received := 0
	timeout := time.After(time.Second)
	for received < 3 {
		select {
		case <-wild.Ch:
			received++
		case <-timeout:
			t.Fatalf("wildcard: expected 3 events, got %d", received)
		}
	}

	select {
	case <-sub.Ch:
	default:
		t.Fatal("alpha subscriber should have received alpha event")
	}
}

func TestEventBus_DroppedEvent(t *testing.T) {
	bus := NewEventBus()
	defer bus.Close()

	_ = bus.Subscribe("metric", 1)

	bus.Emit("metric", "a")
	bus.Emit("metric", "b")
	bus.Emit("metric", "c")

	dropped := bus.Dropped()
	if dropped["metric"] < 1 {
		t.Fatalf("expected dropped metric >= 1, got %d", dropped["metric"])
	}
}

func TestEventBus_Unsubscribe(t *testing.T) {
	bus := NewEventBus()
	defer bus.Close()

	sub := bus.Subscribe("x", 4)
	bus.Emit("x", "before")

	select {
	case evt := <-sub.Ch:
		if evt.Payload.(string) != "before" {
			t.Fatalf("expected before, got %v", evt.Payload)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out receiving before event")
	}

	bus.Unsubscribe(sub)

	bus.Emit("x", "after")

	select {
	case evt := <-sub.Ch:
		t.Fatalf("should not receive after unsubscribe, got %v", evt)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestEventBus_Close(t *testing.T) {
	bus := NewEventBus()

	sub := bus.Subscribe("z", 4)
	bus.Emit("z", "data")

	bus.Close()

	_, ok := <-sub.Ch
	if ok {
		t.Fatal("channel should be closed after Close")
	}

	dropped := bus.Dropped()
	if dropped["z"] != 0 {
		t.Fatalf("expected no drops for z, got %d", dropped["z"])
	}
}

func TestEventBus_ConcurrentSafety(t *testing.T) {
	bus := NewEventBus()
	defer bus.Close()

	var wg sync.WaitGroup
	subs := make([]*Subscriber, 10)
	for i := range subs {
		subs[i] = bus.Subscribe("stress", 64)
	}

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			bus.Emit("stress", i)
		}()
	}

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			bus.Unsubscribe(subs[idx])
		}(i)
	}

	wg.Wait()
}

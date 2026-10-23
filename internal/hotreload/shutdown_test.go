package hotreload

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestWireAgentStopOwnsConsumerLifetime(t *testing.T) {
	bus := New(Paths{})
	stop, err := WireAgent(context.Background(), bus, &fakeAgent{}, filepath.Join(t.TempDir(), "user.yaml"), DiscardReporter())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		var callers sync.WaitGroup
		for range 10 {
			callers.Go(stop)
		}
		callers.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("unsubscribe waited forever with a live parent context")
	}
	bus.mu.Lock()
	defer bus.mu.Unlock()
	if len(bus.handlers[SubjectConfig]) != 0 {
		t.Fatal("consumer subscription was retained after stop")
	}
}

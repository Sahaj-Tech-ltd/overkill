package gateway

import (
	"context"
	"fmt"
	"testing"
	"time"
)

type boundLockAgent struct{ *fakeAgent }

func (a *boundLockAgent) AgentForSession(context.Context, string) (AgentSender, error) {
	return a.fakeAgent, nil
}

func TestBoundSessionLocksAvoidCollisionsAndRetainSameSessionExclusion(t *testing.T) {
	d := NewDispatcher(&boundLockAgent{&fakeAgent{}}, nil)
	const first = "held-session"
	collision := ""
	for i := 0; i < 10000; i++ {
		candidate := fmt.Sprintf("other-%d", i)
		if d.stripeFor(candidate) == d.stripeFor(first) {
			collision = candidate
			break
		}
	}
	if collision == "" {
		t.Fatal("could not construct colliding legacy stripe")
	}
	held, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() { d.serialize(first, func() { close(held); <-release }); close(done) }()
	<-held
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
		<-done
	}()
	other := make(chan struct{})
	go d.serialize(collision, func() { close(other) })
	select {
	case <-other:
	case <-time.After(time.Second):
		t.Fatal("unrelated session waited for a colliding long-running turn")
	}
	same := make(chan struct{})
	go d.serialize(first, func() { close(same) })
	select {
	case <-same:
		t.Fatal("same-session serialization was lost")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	select {
	case <-same:
	case <-time.After(time.Second):
		t.Fatal("same-session waiter did not resume")
	}
	<-done
	// Wait for both functions' deferred reference cleanup, not only their callbacks.
	deadline := time.Now().Add(time.Second)
	for {
		d.boundLocksMu.Lock()
		remaining := len(d.boundLocks)
		d.boundLocksMu.Unlock()
		if remaining == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("completed session locks were retained")
		}
		time.Sleep(time.Millisecond)
	}
}

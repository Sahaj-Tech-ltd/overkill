package web

import "sync"

// eventBus is a tiny pub/sub. Every WebSocket subscriber gets a copy of every
// published event; the subscription channel is buffered, and slow subscribers
// drop events rather than blocking the publisher (best-effort delivery — the
// browser can request a session refresh if it suspects it missed something).
type eventBus struct {
	mu   sync.RWMutex
	subs map[*subscription]struct{}
}

type subscription struct {
	ch chan wsEvent
}

func newEventBus() *eventBus {
	return &eventBus{subs: make(map[*subscription]struct{})}
}

func (b *eventBus) subscribe() *subscription {
	sub := &subscription{ch: make(chan wsEvent, 64)}
	b.mu.Lock()
	b.subs[sub] = struct{}{}
	b.mu.Unlock()
	return sub
}

func (b *eventBus) unsubscribe(sub *subscription) {
	b.mu.Lock()
	if _, ok := b.subs[sub]; ok {
		delete(b.subs, sub)
		close(sub.ch)
	}
	b.mu.Unlock()
}

// publish fans the event out. sessionID is included on the event so per-tab
// filtering happens client-side; this keeps the bus dumb.
func (b *eventBus) publish(sessionID string, ev wsEvent) {
	if ev.SessionID == "" {
		ev.SessionID = sessionID
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	for sub := range b.subs {
		select {
		case sub.ch <- ev:
		default:
			// slow subscriber — drop and keep going
		}
	}
}

package agent

import (
	"sync"
	"sync/atomic"
)

type EventKind string

type BusEvent struct {
	Kind    EventKind
	Payload interface{}
}

type Subscriber struct {
	ID     string
	Kind   EventKind
	Ch     chan BusEvent
	closed atomic.Bool
}

type EventBus struct {
	mu         sync.RWMutex
	subs       []*Subscriber
	dropped    map[EventKind]*atomic.Int64
	droppedMu  sync.Mutex
	closed     bool
	bufferSize int
}

type EventBusOption func(*EventBus)

func WithBufferSize(n int) EventBusOption {
	return func(b *EventBus) {
		b.bufferSize = n
	}
}

func NewEventBus(opts ...EventBusOption) *EventBus {
	b := &EventBus{
		subs:    make([]*Subscriber, 0),
		dropped: make(map[EventKind]*atomic.Int64),
	}
	for _, opt := range opts {
		opt(b)
	}
	return b
}

func (b *EventBus) Subscribe(kind EventKind, bufSize int) *Subscriber {
	sub := &Subscriber{
		Kind: kind,
		Ch:   make(chan BusEvent, bufSize),
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subs = append(b.subs, sub)
	return sub
}

func (b *EventBus) Unsubscribe(sub *Subscriber) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i, s := range b.subs {
		if s == sub {
			b.subs = append(b.subs[:i], b.subs[i+1:]...)
			sub.closed.Store(true)
			return
		}
	}
}

func (b *EventBus) Emit(kind EventKind, payload interface{}) {
	evt := BusEvent{Kind: kind, Payload: payload}
	b.mu.RLock()
	defer b.mu.RUnlock()
	for _, sub := range b.subs {
		if sub.Kind != "" && sub.Kind != kind {
			continue
		}
		if sub.closed.Load() {
			continue
		}
		select {
		case sub.Ch <- evt:
		default:
			b.incrementDropped(kind)
		}
	}
}

func (b *EventBus) Dropped() map[EventKind]int64 {
	b.droppedMu.Lock()
	defer b.droppedMu.Unlock()
	result := make(map[EventKind]int64, len(b.dropped))
	for k, v := range b.dropped {
		result[k] = v.Load()
	}
	return result
}

func (b *EventBus) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, sub := range b.subs {
		sub.closed.Store(true)
	Drain:
		for {
			select {
			case <-sub.Ch:
			default:
				break Drain
			}
		}
		close(sub.Ch)
	}
	b.subs = b.subs[:0]
	b.closed = true
}

func (b *EventBus) incrementDropped(kind EventKind) {
	b.droppedMu.Lock()
	counter, ok := b.dropped[kind]
	if !ok {
		counter = &atomic.Int64{}
		b.dropped[kind] = counter
	}
	b.droppedMu.Unlock()
	counter.Add(1)
}

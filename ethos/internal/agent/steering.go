package agent

import (
	"context"
	"sort"
	"sync"
)

type SteeringMode int

const (
	SteeringOneAtATime SteeringMode = iota
	SteeringDrainAll
)

type SteeredMessage struct {
	Content  string
	Role     string
	Priority int
}

type SteeringQueue struct {
	queue  []SteeredMessage
	mode   SteeringMode
	mu     sync.Mutex
	cond   *sync.Cond
	closed bool
}

func NewSteeringQueue(mode SteeringMode) *SteeringQueue {
	sq := &SteeringQueue{
		queue: make([]SteeredMessage, 0),
		mode:  mode,
	}
	sq.cond = sync.NewCond(&sq.mu)
	return sq
}

func (sq *SteeringQueue) Inject(msg SteeredMessage) {
	sq.mu.Lock()
	defer sq.mu.Unlock()
	i := sort.Search(len(sq.queue), func(i int) bool {
		return sq.queue[i].Priority < msg.Priority
	})
	sq.queue = append(sq.queue, SteeredMessage{})
	copy(sq.queue[i+1:], sq.queue[i:])
	sq.queue[i] = msg
	sq.cond.Broadcast()
}

func (sq *SteeringQueue) Drained() []SteeredMessage {
	sq.mu.Lock()
	defer sq.mu.Unlock()
	if len(sq.queue) == 0 {
		return nil
	}
	var result []SteeredMessage
	switch sq.mode {
	case SteeringOneAtATime:
		result = []SteeredMessage{sq.queue[0]}
		sq.queue = sq.queue[1:]
	case SteeringDrainAll:
		result = sq.queue
		sq.queue = make([]SteeredMessage, 0)
	}
	return result
}

func (sq *SteeringQueue) Pending() int {
	sq.mu.Lock()
	defer sq.mu.Unlock()
	return len(sq.queue)
}

func (sq *SteeringQueue) Wait(ctx context.Context) error {
	// Old implementation:
	//   1. Mutated `sq.closed = true` on ctx cancel — permanently
	//      closing the queue for every future Wait call. One
	//      cancelled Wait killed the whole queue's usefulness.
	//   2. Had a set-then-broadcast race: if Broadcast fired before
	//      the waiter goroutine had reached cond.Wait, the signal
	//      was lost and the goroutine leaked.
	//
	// New implementation uses a per-call cancel goroutine driven by
	// ctx that calls Broadcast on cancel; the waiter goroutine
	// checks ctx.Err() in its loop predicate, so a missed Broadcast
	// is harmless — the next signal (Inject, Close, or the cancel
	// goroutine's broadcast) wakes it and the ctx check kicks it out.
	done := make(chan struct{})
	cancelWatcher := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			// Broadcast under the cond's mutex so any in-flight
			// Wait observes the ctx state before the next predicate
			// re-check. Without the lock-then-broadcast pattern,
			// cond.Wait can re-enter just before we'd have woken it.
			sq.mu.Lock()
			sq.cond.Broadcast()
			sq.mu.Unlock()
		case <-cancelWatcher:
		}
	}()
	go func() {
		sq.mu.Lock()
		defer sq.mu.Unlock()
		for len(sq.queue) == 0 && !sq.closed && ctx.Err() == nil {
			sq.cond.Wait()
		}
		close(done)
	}()
	<-done
	close(cancelWatcher)
	return ctx.Err()
}

func (sq *SteeringQueue) Close() {
	sq.mu.Lock()
	defer sq.mu.Unlock()
	sq.closed = true
	sq.cond.Broadcast()
}

func (sq *SteeringQueue) Clear() {
	sq.mu.Lock()
	defer sq.mu.Unlock()
	sq.queue = make([]SteeredMessage, 0)
}

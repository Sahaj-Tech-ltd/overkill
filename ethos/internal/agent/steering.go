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
	queue []SteeredMessage
	mode  SteeringMode
	mu    sync.Mutex
	cond  *sync.Cond
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
	done := make(chan struct{})
	go func() {
		sq.mu.Lock()
		defer sq.mu.Unlock()
		for len(sq.queue) == 0 && !sq.closed {
			sq.cond.Wait()
		}
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		sq.cond.Broadcast()
		sq.mu.Lock()
		sq.closed = true
		sq.mu.Unlock()
		sq.cond.Broadcast()
		return ctx.Err()
	}
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

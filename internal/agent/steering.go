package agent

import (
	"context"
	"sort"
	"strings"
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
	notify chan struct{} // non-blocking signal on Inject/Close
	closed bool
}

func NewSteeringQueue(mode SteeringMode) *SteeringQueue {
	sq := &SteeringQueue{
		queue:  make([]SteeredMessage, 0),
		mode:   mode,
		notify: make(chan struct{}, 1),
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
	// Non-blocking signal for channel-based Wait.
	select {
	case sq.notify <- struct{}{}:
	default:
	}
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
	for {
		// Check pending under lock.
		sq.mu.Lock()
		if len(sq.queue) > 0 {
			sq.mu.Unlock()
			return nil // items available — drain before honoring ctx
		}
		if sq.closed {
			sq.mu.Unlock()
			return ctx.Err() // no more items will arrive
		}
		sq.mu.Unlock()

		// Wait for notification or ctx done.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-sq.notify:
			// Loop back to re-check queue.
		}
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

// Append adds a simple string steering message to the queue.
// The message is stored with Role="system" and Priority=0.
func (sq *SteeringQueue) Append(msg string) {
	sq.Inject(SteeredMessage{Content: msg, Role: "system"})
}

// Drain returns the oldest message as a plain string (FIFO) and
// removes it from the queue. Returns empty string if the queue
// is empty.
func (sq *SteeringQueue) Drain() string {
	sq.mu.Lock()
	defer sq.mu.Unlock()
	if len(sq.queue) == 0 {
		return ""
	}
	result := sq.queue[0]
	sq.queue = sq.queue[1:]
	return result.Content
}

// DrainAll returns all queued messages combined into a single
// newline-separated string, then empties the queue. Returns
// empty string if the queue is empty.
func (sq *SteeringQueue) DrainAll() string {
	sq.mu.Lock()
	defer sq.mu.Unlock()
	if len(sq.queue) == 0 {
		return ""
	}
	var b strings.Builder
	for i, m := range sq.queue {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(m.Content)
	}
	sq.queue = make([]SteeredMessage, 0)
	return b.String()
}

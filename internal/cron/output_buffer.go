package cron

import (
	"sync"
	"time"
)

// queuedOutput holds a single cron job output that arrived while the
// agent was busy and is waiting for a quiet window to be delivered.
type queuedOutput struct {
	job    *Job
	output string
}

// OutputBuffer holds queued cron output when the agent is busy.
// When the agent becomes idle (tracker.IdleFor returns true for the
// configured window), all queued outputs are flushed via onFlush in
// FIFO order.
type OutputBuffer struct {
	mu         sync.Mutex
	queue      []queuedOutput
	idleWindow time.Duration
	tracker    *ActivityTracker
	onFlush    func(job *Job, output string)
	stop       chan struct{}
	done       chan struct{}
}

// NewOutputBuffer creates a buffer that checks idle status against the
// given tracker. When idleWindow has passed since the last activity,
// queued outputs are flushed via onFlush. A background goroutine polls
// every 30 seconds and auto-flushes when the idle window is met.
func NewOutputBuffer(idleWindow time.Duration, tracker *ActivityTracker, onFlush func(job *Job, output string)) *OutputBuffer {
	b := &OutputBuffer{
		idleWindow: idleWindow,
		tracker:    tracker,
		onFlush:    onFlush,
		stop:       make(chan struct{}),
		done:       make(chan struct{}),
	}
	go b.flushLoop()
	return b
}

// MaybeFire delivers output immediately if the agent is idle, or
// queues it for later delivery if the agent is busy.
func (b *OutputBuffer) MaybeFire(j *Job, output string) {
	if b.tracker.IdleFor(b.idleWindow) {
		// Agent is idle — deliver immediately.
		if b.onFlush != nil {
			b.onFlush(j, output)
		}
		return
	}
	// Agent is busy — queue.
	b.mu.Lock()
	b.queue = append(b.queue, queuedOutput{job: j, output: output})
	b.mu.Unlock()
}

// flushLoop polls every 30s; when the idle window is met, it flushes
// everything queued.
func (b *OutputBuffer) flushLoop() {
	defer close(b.done)
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-b.stop:
			// Drain remaining on shutdown so no output is lost.
			b.flushAll()
			return
		case <-ticker.C:
			if b.tracker.IdleFor(b.idleWindow) {
				b.flushAll()
			}
		}
	}
}

// flushAll drains the queue and calls onFlush for each entry.
func (b *OutputBuffer) flushAll() {
	b.mu.Lock()
	queue := b.queue
	b.queue = nil
	b.mu.Unlock()
	for _, q := range queue {
		if b.onFlush != nil {
			b.onFlush(q.job, q.output)
		}
	}
}

// Stop shuts down the background flush goroutine. Call once before the
// buffer goes out of scope.
func (b *OutputBuffer) Stop() {
	close(b.stop)
	<-b.done
}

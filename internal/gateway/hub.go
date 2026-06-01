package gateway

import (
	"context"
	"fmt"
	"io"
	"log"
	"runtime/debug"
	"sync"
	"time"
)

// Hub runs N channels concurrently, restarts ones that crash, and
// shuts them all down cleanly when ctx is cancelled.
type Hub struct {
	mu       sync.RWMutex
	Channels []Channel
	Logger   *log.Logger
	// BackoffInitial is the initial backoff duration for channel
	// restart attempts. Zero falls back to 1s.
	BackoffInitial time.Duration
	// BackoffMax is the maximum backoff cap for channel restart
	// attempts. Zero falls back to 30s.
	BackoffMax time.Duration
}

// NewHub returns a Hub with logging defaulted to discard.
// Nil channels are filtered out.
func NewHub(channels ...Channel) *Hub {
	filtered := make([]Channel, 0, len(channels))
	for _, c := range channels {
		if c != nil {
			filtered = append(filtered, c)
		}
	}
	return &Hub{Channels: filtered, Logger: log.New(io.Discard, "", 0)}
}

// Add appends a channel to the hub. Must be called before Run.
func (h *Hub) Add(c Channel) {
	if c == nil {
		return
	}
	h.mu.Lock()
	h.Channels = append(h.Channels, c)
	h.mu.Unlock()
}

// Run blocks until ctx is cancelled. Each channel runs in its own
// goroutine under a supervise loop with exponential backoff: a channel
// that returns (a panic or a non-ctx error) is restarted, capped at
// hubMaxBackoff between attempts. Old code logged the exit and let the
// goroutine end — one Discord/Telegram panic permanently killed that
// channel until the whole process was restarted.
func (h *Hub) Run(ctx context.Context) error {
	h.mu.RLock()
	chs := make([]Channel, len(h.Channels))
	copy(chs, h.Channels)
	h.mu.RUnlock()
	if len(chs) == 0 {
		return fmt.Errorf("gateway: hub has no channels configured")
	}
	var wg sync.WaitGroup
	for _, c := range chs {
		c := c
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					h.Logger.Printf("hub: channel goroutine panic: %v\n%s", r, debug.Stack())
				}
			}()
			h.superviseChannel(ctx, c)
		}()
	}
	wg.Wait()
	return ctx.Err()
}

func (h *Hub) superviseChannel(ctx context.Context, c Channel) {
	initial := h.BackoffInitial
	if initial <= 0 {
		initial = 1 * time.Second
	}
	cap := h.BackoffMax
	if cap <= 0 {
		cap = 30 * time.Second
	}
	backoff := initial
	for {
		if ctx.Err() != nil {
			return
		}
		h.Logger.Printf("hub: %s starting", c.Name())
		err := h.runOnce(ctx, c)
		if ctx.Err() != nil {
			if err != nil {
				h.Logger.Printf("hub: %s stopped (ctx): %v", c.Name(), err)
			}
			return
		}
		if err != nil {
			h.Logger.Printf("hub: %s exited: %v — restarting in %s", c.Name(), err, backoff)
		} else {
			h.Logger.Printf("hub: %s exited cleanly — restarting in %s", c.Name(), backoff)
			backoff = initial // reset after successful run
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		// Exponential up to cap.
		if err != nil {
			backoff *= 2
			if backoff > cap {
				backoff = cap
			}
		}
	}
}

// runOnce wraps c.Run with a panic recover so a misbehaving channel
// implementation (or its SDK) can't take out the supervise loop.
func (h *Hub) runOnce(ctx context.Context, c Channel) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return c.Run(ctx)
}

// NotifierBots filters a slice of Channel to those that also
// implement Notifier, enabling the §7.1 Layer 6 completion-push
// poller and other cron/alert systems to deliver notifications
// through already-open gateway connections.
func NotifierBots(channels []Channel) []Notifier {
	var out []Notifier
	for _, c := range channels {
		if n, ok := c.(Notifier); ok {
			out = append(out, n)
		}
	}
	return out
}

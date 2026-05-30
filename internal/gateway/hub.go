package gateway

import (
	"context"
	"fmt"
	"io"
	"log"
	"sync"
	"time"
)

// Hub runs N channels concurrently, restarts ones that crash, and
// shuts them all down cleanly when ctx is cancelled.
type Hub struct {
	Channels []Channel
	Logger   *log.Logger
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
	h.Channels = append(h.Channels, c)
}

// Run blocks until ctx is cancelled. Each channel runs in its own
// goroutine under a supervise loop with exponential backoff: a channel
// that returns (a panic or a non-ctx error) is restarted, capped at
// hubMaxBackoff between attempts. Old code logged the exit and let the
// goroutine end — one Discord/Telegram panic permanently killed that
// channel until the whole process was restarted.
func (h *Hub) Run(ctx context.Context) error {
	if len(h.Channels) == 0 {
		return fmt.Errorf("gateway: hub has no channels configured")
	}
	var wg sync.WaitGroup
	for _, c := range h.Channels {
		c := c
		wg.Add(1)
		go func() {
			defer wg.Done()
			h.superviseChannel(ctx, c)
		}()
	}
	wg.Wait()
	return ctx.Err()
}

const (
	hubInitialBackoff = 1 * time.Second
	hubMaxBackoff     = 30 * time.Second
)

func (h *Hub) superviseChannel(ctx context.Context, c Channel) {
	backoff := hubInitialBackoff
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
			backoff = hubInitialBackoff // reset after successful run
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		// Exponential up to cap.
		if err != nil {
			backoff *= 2
			if backoff > hubMaxBackoff {
				backoff = hubMaxBackoff
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

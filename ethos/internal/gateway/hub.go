package gateway

import (
	"context"
	"fmt"
	"io"
	"log"
	"sync"
)

// Hub runs N channels concurrently, restarts ones that crash, and
// shuts them all down cleanly when ctx is cancelled.
type Hub struct {
	Channels []Channel
	Logger   *log.Logger
}

// NewHub returns a Hub with logging defaulted to discard.
func NewHub(channels ...Channel) *Hub {
	return &Hub{Channels: channels, Logger: log.New(io.Discard, "", 0)}
}

// Add appends a channel to the hub. Must be called before Run.
func (h *Hub) Add(c Channel) {
	if c == nil {
		return
	}
	h.Channels = append(h.Channels, c)
}

// Run blocks until ctx is cancelled. Each channel runs in its own
// goroutine; a channel returning a non-context error is logged but does
// not bring down the hub.
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
			h.Logger.Printf("hub: %s starting", c.Name())
			if err := c.Run(ctx); err != nil && ctx.Err() == nil {
				h.Logger.Printf("hub: %s exited: %v", c.Name(), err)
			}
		}()
	}
	wg.Wait()
	return ctx.Err()
}

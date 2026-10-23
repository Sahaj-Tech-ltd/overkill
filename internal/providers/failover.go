package providers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

type FailoverChain struct {
	providers []Provider
	cooldowns map[string]time.Time
	mu        sync.RWMutex
}

func NewFailoverChain(providers ...Provider) *FailoverChain {
	return &FailoverChain{
		providers: providers,
		cooldowns: make(map[string]time.Time),
	}
}

func (fc *FailoverChain) Complete(ctx context.Context, req Request) (Response, error) {
	if err := ctx.Err(); err != nil {
		return Response{}, fmt.Errorf("providers: %w", err)
	}

	providers := fc.availableProviders()

	if len(providers) == 0 {
		providers = fc.leastRecentlyFailed()
	}

	if len(providers) == 0 {
		return Response{}, ErrProviderUnavailable
	}

	var lastErr error
	for _, p := range providers {
		resp, err := p.Complete(ctx, req)
		if err == nil {
			fc.ResetCooldown(p.Name())
			return resp, nil
		}
		if !fc.recordFailure(ctx, p.Name(), err) {
			return resp, err
		}
		lastErr = err
		log.Warn().
			Err(err).
			Str("provider", p.Name()).
			Msg("provider failed, trying next")
	}

	return Response{}, fmt.Errorf("providers: all providers failed: %w", lastErr)
}

func (fc *FailoverChain) Stream(ctx context.Context, req Request) (<-chan Chunk, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("providers: %w", err)
	}

	providers := fc.availableProviders()

	if len(providers) == 0 {
		providers = fc.leastRecentlyFailed()
	}

	if len(providers) == 0 {
		return nil, ErrProviderUnavailable
	}

	var lastErr error
	for _, p := range providers {
		ch, err := p.Stream(ctx, req)
		if err == nil {
			if ch == nil {
				err = fmt.Errorf("provider returned a nil stream")
			} else {
				return fc.observeStream(ctx, p.Name(), ch), nil
			}
		}
		if !fc.recordFailure(ctx, p.Name(), err) {
			return nil, err
		}
		lastErr = err
		log.Warn().
			Err(err).
			Str("provider", p.Name()).
			Msg("provider stream failed, trying next")
	}

	return nil, fmt.Errorf("providers: all providers failed: %w", lastErr)
}

// recordFailure returns false only for caller cancellation. Request-specific
// validation failures may use another provider, but do not mark a healthy
// provider unavailable for unrelated requests.
func (fc *FailoverChain) recordFailure(ctx context.Context, name string, err error) bool {
	if ctx.Err() != nil || errors.Is(err, context.Canceled) {
		return false
	}
	classified := NewErrorClassifier().Classify(err)
	switch classified.Reason {
	case ReasonContextOverflow, ReasonModelNotFound:
		return true
	case ReasonFormat:
		fc.MarkFailed(name, 5*time.Second)
	case ReasonAuth:
		fc.MarkFailed(name, time.Minute)
	default:
		if classified.Cooldown > 0 {
			fc.MarkFailed(name, classified.Cooldown)
		}
	}
	return true
}

// Once a stream is published, never retry model work on another provider: the
// consumer may already have acted on its content. Observe its eventual health
// while forwarding real usage and failure receipts unchanged.
func (fc *FailoverChain) observeStream(ctx context.Context, name string, input <-chan Chunk) <-chan Chunk {
	out := make(chan Chunk, 16)
	go func() {
		defer close(out)
		for {
			select {
			case <-ctx.Done():
				return
			case chunk, ok := <-input:
				if !ok {
					if ctx.Err() == nil {
						err := fmt.Errorf("provider stream closed without completion: %w", io.ErrUnexpectedEOF)
						fc.MarkFailed(name, 5*time.Second)
						_ = sendChunk(ctx, out, Chunk{Err: err})
					}
					return
				}
				if chunk.Err != nil {
					fc.recordFailure(ctx, name, chunk.Err)
					_ = sendChunk(ctx, out, chunk)
					return
				}
				if chunk.Done {
					fc.ResetCooldown(name)
					_ = sendChunk(ctx, out, chunk)
					return
				}
				if !sendChunk(ctx, out, chunk) {
					return
				}
			}
		}
	}()
	return out
}

func (fc *FailoverChain) MarkFailed(name string, cooldown time.Duration) {
	fc.mu.Lock()
	defer fc.mu.Unlock()

	if cooldown <= 0 {
		cooldown = 60 * time.Second
	}
	fc.cooldowns[name] = time.Now().Add(cooldown)
}

func (fc *FailoverChain) ResetCooldown(name string) {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	delete(fc.cooldowns, name)
}

func (fc *FailoverChain) availableProviders() []Provider {
	fc.mu.RLock()
	defer fc.mu.RUnlock()

	now := time.Now()
	var available []Provider
	for _, p := range fc.providers {
		if expiry, ok := fc.cooldowns[p.Name()]; !ok || now.After(expiry) {
			available = append(available, p)
		}
	}
	return available
}

func (fc *FailoverChain) leastRecentlyFailed() []Provider {
	fc.mu.RLock()
	defer fc.mu.RUnlock()

	var oldest time.Time
	var picked Provider

	for _, p := range fc.providers {
		expiry, ok := fc.cooldowns[p.Name()]
		if !ok {
			continue
		}
		if picked == nil || expiry.Before(oldest) {
			oldest = expiry
			picked = p
		}
	}

	if picked == nil {
		return nil
	}
	return []Provider{picked}
}

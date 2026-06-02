package providers

import (
	"context"
	"fmt"
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
			fc.ResetCooldown(p.Name())
			return ch, nil
		}
		lastErr = err
		log.Warn().
			Err(err).
			Str("provider", p.Name()).
			Msg("provider stream failed, trying next")
	}

	return nil, fmt.Errorf("providers: all providers failed: %w", lastErr)
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

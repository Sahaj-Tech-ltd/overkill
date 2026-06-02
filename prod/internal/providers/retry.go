package providers

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"math"
	mathrand "math/rand"
	"time"

	"github.com/rs/zerolog/log"
)

const maxRetries = 8

const (
	defaultBaseDelay = 2 * time.Second
	delayGrowth      = 2
	jitterFactor     = 0.2
	maxRetryDelay    = 30 * time.Second
)

type retryAfter interface {
	RetryAfter() time.Duration
}

type retryConfig struct {
	baseDelay time.Duration
}

// WithRetry is a convenience wrapper that uses context.Background().
// Prefer WithRetryCtx for cancellation support.
func WithRetry(fn func() (*Response, error), isRetryable func(error) bool) (*Response, error) {
	return WithRetryCtx(context.Background(), fn, isRetryable)
}

// WithRetryStream is a convenience wrapper that uses context.Background().
// Prefer WithRetryStreamCtx for cancellation support.
func WithRetryStream(fn func() (<-chan Chunk, error), isRetryable func(error) bool) (<-chan Chunk, error) {
	return WithRetryStreamCtx(context.Background(), fn, isRetryable)
}

func WithRetryCtx(ctx context.Context, fn func() (*Response, error), isRetryable func(error) bool) (*Response, error) {
	return withRetry(ctx, fn, isRetryable, retryConfig{baseDelay: defaultBaseDelay})
}

func WithRetryStreamCtx(ctx context.Context, fn func() (<-chan Chunk, error), isRetryable func(error) bool) (<-chan Chunk, error) {
	return withRetryStream(ctx, fn, isRetryable, retryConfig{baseDelay: defaultBaseDelay})
}

func withRetry(ctx context.Context, fn func() (*Response, error), isRetryable func(error) bool, cfg retryConfig) (*Response, error) {
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		resp, err := fn()
		if err == nil {
			return resp, nil
		}
		lastErr = err

		if !isRetryable(err) {
			return nil, err
		}

		delay := calculateDelay(attempt, cfg.baseDelay)

		if ra, ok := err.(retryAfter); ok {
			if after := ra.RetryAfter(); after > 0 && after > delay {
				delay = after
			}
		}

		log.Warn().
			Err(err).
			Int("attempt", attempt+1).
			Dur("delay", delay).
			Msg("retrying after error")

		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return nil, lastErr
}

func withRetryStream(ctx context.Context, fn func() (<-chan Chunk, error), isRetryable func(error) bool, cfg retryConfig) (<-chan Chunk, error) {
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		ch, err := fn()
		if err == nil {
			return ch, nil
		}
		lastErr = err

		if !isRetryable(err) {
			return nil, err
		}

		delay := calculateDelay(attempt, cfg.baseDelay)

		if ra, ok := err.(retryAfter); ok {
			if after := ra.RetryAfter(); after > 0 && after > delay {
				delay = after
			}
		}

		log.Warn().
			Err(err).
			Int("attempt", attempt+1).
			Dur("delay", delay).
			Msg("retrying stream after error")

		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return nil, lastErr
}

func calculateDelay(attempt int, base time.Duration) time.Duration {
	delay := base
	for i := 0; i < attempt; i++ {
		delay *= delayGrowth
	}
	if delay > maxRetryDelay {
		delay = maxRetryDelay
	}
	jitter := time.Duration(cryptoRandFloat() * jitterFactor * float64(delay))
	return delay + jitter
}

// cryptoRandFloat returns a float in [0, 1) using crypto/rand.
// Thread-safe and unpredictable — suitable for concurrent retry jitter.
func cryptoRandFloat() float64 {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// Fallback: use math/rand as secondary source to avoid
		// deterministic (0.5) jitter and thundering-herd under failure.
		return mathrand.Float64()
	}
	// Convert to uint64 and scale to [0, 1)
	n := binary.BigEndian.Uint64(buf[:])
	return float64(n) / float64(math.MaxUint64)
}

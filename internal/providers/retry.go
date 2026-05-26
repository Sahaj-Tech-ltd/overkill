package providers

import (
	"math/rand"
	"time"

	"github.com/rs/zerolog/log"
)

const maxRetries = 8

const (
	defaultBaseDelay = 2 * time.Second
	delayGrowth      = 2
	jitterFactor     = 0.2
)

type retryAfter interface {
	RetryAfter() time.Duration
}

type retryConfig struct {
	baseDelay time.Duration
}

func WithRetry(fn func() (*Response, error), isRetryable func(error) bool) (*Response, error) {
	return withRetry(fn, isRetryable, retryConfig{baseDelay: defaultBaseDelay})
}

func WithRetryStream(fn func() (<-chan Chunk, error), isRetryable func(error) bool) (<-chan Chunk, error) {
	return withRetryStream(fn, isRetryable, retryConfig{baseDelay: defaultBaseDelay})
}

func withRetry(fn func() (*Response, error), isRetryable func(error) bool, cfg retryConfig) (*Response, error) {
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

		time.Sleep(delay)
	}
	return nil, lastErr
}

func withRetryStream(fn func() (<-chan Chunk, error), isRetryable func(error) bool, cfg retryConfig) (<-chan Chunk, error) {
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

		time.Sleep(delay)
	}
	return nil, lastErr
}

func calculateDelay(attempt int, base time.Duration) time.Duration {
	delay := base
	for i := 0; i < attempt; i++ {
		delay *= delayGrowth
	}
	jitter := time.Duration(rand.Float64() * jitterFactor * float64(delay))
	return delay + jitter
}

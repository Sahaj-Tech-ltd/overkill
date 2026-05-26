package providers

import (
	"errors"
	"fmt"
	"net/http"
)

var (
	ErrProviderUnavailable = errors.New("provider: unavailable")
	ErrModelNotFound       = errors.New("provider: model not found")
	ErrRateLimited         = errors.New("provider: rate limited")
	ErrContextLength       = errors.New("provider: context length exceeded")
	ErrAuthFailed          = errors.New("provider: authentication failed")
)

type HTTPError struct {
	StatusCode int
	Body       string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("provider: HTTP %d: %s", e.StatusCode, e.Body)
}

func (e *HTTPError) IsRetryable() bool {
	switch e.StatusCode {
	case http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		529:
		return true
	default:
		return false
	}
}

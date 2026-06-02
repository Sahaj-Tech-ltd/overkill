package providers

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestClassifier_AuthErrors(t *testing.T) {
	c := NewErrorClassifier()

	cases := []struct {
		errMsg    string
		retryable bool
		cooldown  time.Duration
	}{
		{"HTTP 401: unauthorized access", false, 0},
		{"HTTP 403: forbidden", false, 0},
		{"invalid api key provided", false, 0},
		{"authentication failed for provider", false, 0},
		{"unauthorized: token expired", false, 0},
		{"forbidden: insufficient permissions", false, 0},
		{"access denied to resource", false, 0},
	}

	for _, tc := range cases {
		classified := c.Classify(fmt.Errorf("%s", tc.errMsg))
		if classified.Reason != ReasonAuth {
			t.Errorf("expected ReasonAuth for %q, got %s", tc.errMsg, classified.Reason)
		}
		if classified.Retryable != tc.retryable {
			t.Errorf("expected retryable=%v for %q, got %v", tc.retryable, tc.errMsg, classified.Retryable)
		}
		if classified.Cooldown != tc.cooldown {
			t.Errorf("expected cooldown=%v for %q, got %v", tc.cooldown, tc.errMsg, classified.Cooldown)
		}
	}
}

func TestClassifier_RateLimitErrors(t *testing.T) {
	c := NewErrorClassifier()

	cases := []struct {
		errMsg    string
		retryable bool
		cooldown  time.Duration
	}{
		{"HTTP 429: too many requests", true, 60 * time.Second},
		{"rate limit exceeded", true, 60 * time.Second},
		{"too many requests in window", true, 60 * time.Second},
		{"quota exceeded for tier", true, 60 * time.Second},
		{"requests per minute exceeded", true, 60 * time.Second},
		{"request per second limit hit", true, 60 * time.Second},
	}

	for _, tc := range cases {
		classified := c.Classify(fmt.Errorf("%s", tc.errMsg))
		if classified.Reason != ReasonRateLimit {
			t.Errorf("expected ReasonRateLimit for %q, got %s", tc.errMsg, classified.Reason)
		}
		if classified.Retryable != tc.retryable {
			t.Errorf("expected retryable=%v for %q, got %v", tc.retryable, tc.errMsg, classified.Retryable)
		}
		if classified.Cooldown != tc.cooldown {
			t.Errorf("expected cooldown=%v for %q, got %v", tc.cooldown, tc.errMsg, classified.Cooldown)
		}
	}
}

func TestClassifier_BillingErrors(t *testing.T) {
	c := NewErrorClassifier()

	cases := []struct {
		errMsg string
	}{
		{"HTTP 402: payment required"},
		{"billing issue detected"},
		{"payment method expired"},
		{"insufficient funds in account"},
		{"credit card declined"},
		{"subscription has expired"},
		{"plan limit reached"},
	}

	for _, tc := range cases {
		classified := c.Classify(fmt.Errorf("%s", tc.errMsg))
		if classified.Reason != ReasonBilling {
			t.Errorf("expected ReasonBilling for %q, got %s", tc.errMsg, classified.Reason)
		}
		if classified.Retryable {
			t.Errorf("expected retryable=false for %q", tc.errMsg)
		}
		if classified.Cooldown != 24*time.Hour {
			t.Errorf("expected cooldown=24h for %q, got %v", tc.errMsg, classified.Cooldown)
		}
	}
}

func TestClassifier_NetworkErrors(t *testing.T) {
	c := NewErrorClassifier()

	cases := []struct {
		errMsg   string
		cooldown time.Duration
	}{
		{"connection refused by host", 10 * time.Second},
		{"dns resolution failed", 10 * time.Second},
		{"no such host example.com", 10 * time.Second},
		{"network is unreachable", 10 * time.Second},
		{"dial tcp ECONNREFUSED", 10 * time.Second},
		{"read: ECONNRESET", 10 * time.Second},
		{"write: EPIPE broken", 10 * time.Second},
		{"connection reset by peer", 10 * time.Second},
	}

	for _, tc := range cases {
		classified := c.Classify(fmt.Errorf("%s", tc.errMsg))
		if classified.Reason != ReasonNetwork {
			t.Errorf("expected ReasonNetwork for %q, got %s", tc.errMsg, classified.Reason)
		}
		if !classified.Retryable {
			t.Errorf("expected retryable=true for %q", tc.errMsg)
		}
		if classified.Cooldown != tc.cooldown {
			t.Errorf("expected cooldown=%v for %q, got %v", tc.cooldown, tc.errMsg, classified.Cooldown)
		}
	}
}

func TestClassifier_TimeoutErrors(t *testing.T) {
	c := NewErrorClassifier()

	cases := []struct {
		errMsg string
	}{
		{"context deadline exceeded"},
		{"context canceled by caller"},
		{"i/o timeout on read"},
		{"read timeout after 30s"},
		{"deadline exceeded for request"},
	}

	for _, tc := range cases {
		classified := c.Classify(fmt.Errorf("%s", tc.errMsg))
		if classified.Reason != ReasonTimeout {
			t.Errorf("expected ReasonTimeout for %q, got %s", tc.errMsg, classified.Reason)
		}
		if !classified.Retryable {
			t.Errorf("expected retryable=true for %q", tc.errMsg)
		}
		if classified.Cooldown != 30*time.Second {
			t.Errorf("expected cooldown=30s for %q, got %v", tc.errMsg, classified.Cooldown)
		}
	}
}

func TestClassifier_FormatErrors(t *testing.T) {
	c := NewErrorClassifier()

	cases := []string{
		"json: cannot unmarshal object",
		"unmarshal: unexpected type",
		"invalid response from server",
		"unexpected end of JSON input",
		"decode error: invalid character",
		"syntax error in response body",
	}

	for _, msg := range cases {
		classified := c.Classify(fmt.Errorf("%s", msg))
		if classified.Reason != ReasonFormat {
			t.Errorf("expected ReasonFormat for %q, got %s", msg, classified.Reason)
		}
		if classified.Retryable {
			t.Errorf("expected retryable=false for %q", msg)
		}
	}
}

func TestClassifier_ContextOverflowErrors(t *testing.T) {
	c := NewErrorClassifier()

	cases := []string{
		"context length exceeded maximum",
		"maximum context window reached",
		"too many tokens in prompt",
		"token limit exceeded",
		"input is too long for model",
		"max_tokens exceeds model limit",
	}

	for _, msg := range cases {
		classified := c.Classify(fmt.Errorf("%s", msg))
		if classified.Reason != ReasonContextOverflow {
			t.Errorf("expected ReasonContextOverflow for %q, got %s", msg, classified.Reason)
		}
		if classified.Retryable {
			t.Errorf("expected retryable=false for %q", msg)
		}
	}
}

func TestClassifier_OverloadedErrors(t *testing.T) {
	c := NewErrorClassifier()

	cases := []struct {
		errMsg   string
		cooldown time.Duration
	}{
		{"HTTP 500: internal server error", 30 * time.Second},
		{"HTTP 502: bad gateway", 30 * time.Second},
		{"HTTP 503: service unavailable", 30 * time.Second},
		{"HTTP 529: site overloaded", 30 * time.Second},
		{"server overloaded, try again", 30 * time.Second},
		{"internal server error on endpoint", 30 * time.Second},
		{"bad gateway from upstream", 30 * time.Second},
		{"service unavailable temporarily", 30 * time.Second},
	}

	for _, tc := range cases {
		classified := c.Classify(fmt.Errorf("%s", tc.errMsg))
		if classified.Reason != ReasonOverloaded {
			t.Errorf("expected ReasonOverloaded for %q, got %s", tc.errMsg, classified.Reason)
		}
		if !classified.Retryable {
			t.Errorf("expected retryable=true for %q", tc.errMsg)
		}
		if classified.Cooldown != tc.cooldown {
			t.Errorf("expected cooldown=%v for %q, got %v", tc.cooldown, tc.errMsg, classified.Cooldown)
		}
	}
}

func TestClassifier_ModelNotFoundErrors(t *testing.T) {
	c := NewErrorClassifier()

	cases := []string{
		"model not found: gpt-5",
		"model not available in region",
		"model does not exist in catalog",
		"model gpt-5 not supported",
	}

	for _, msg := range cases {
		classified := c.Classify(fmt.Errorf("%s", msg))
		if classified.Reason != ReasonModelNotFound {
			t.Errorf("expected ReasonModelNotFound for %q, got %s", msg, classified.Reason)
		}
		if classified.Retryable {
			t.Errorf("expected retryable=false for %q", msg)
		}
	}
}

func TestClassifier_SyscallPatterns(t *testing.T) {
	c := NewErrorClassifier()

	cases := []string{
		"fork/exec /bin/sh: no such file",
		"signal: killed",
		"signal: aborted",
	}

	for _, msg := range cases {
		classified := c.Classify(fmt.Errorf("%s", msg))
		if classified.Retryable != true {
			t.Errorf("expected retryable=true for %q", msg)
		}
		if classified.Cooldown != 5*time.Second {
			t.Errorf("expected cooldown=5s for %q, got %v", msg, classified.Cooldown)
		}
	}
}

func TestClassifier_UnknownError(t *testing.T) {
	c := NewErrorClassifier()

	classified := c.Classify(fmt.Errorf("something completely unexpected"))
	if classified.Reason != ReasonUnknown {
		t.Errorf("expected ReasonUnknown, got %s", classified.Reason)
	}
	if !classified.Retryable {
		t.Error("expected unknown errors to be retryable as safe default")
	}
	if classified.Cooldown != 5*time.Second {
		t.Errorf("expected cooldown=5s for unknown, got %v", classified.Cooldown)
	}
}

func TestClassifier_NilError(t *testing.T) {
	c := NewErrorClassifier()

	classified := c.Classify(nil)
	if classified.Reason != ReasonUnknown {
		t.Errorf("expected ReasonUnknown for nil, got %s", classified.Reason)
	}
	if classified.Retryable {
		t.Error("expected nil error to not be retryable")
	}
}

func TestClassifier_HTTPError_StatusCodes(t *testing.T) {
	c := NewErrorClassifier()

	tests := []struct {
		statusCode int
		reason     FailoverReason
		retryable  bool
	}{
		{401, ReasonAuth, false},
		{403, ReasonAuth, false},
		{402, ReasonBilling, false},
		{429, ReasonRateLimit, true},
		{500, ReasonOverloaded, true},
		{502, ReasonOverloaded, true},
		{503, ReasonOverloaded, true},
		{529, ReasonOverloaded, true},
		{404, ReasonModelNotFound, false},
		{408, ReasonTimeout, true},
		{413, ReasonContextOverflow, false},
	}

	for _, tc := range tests {
		httpErr := &HTTPError{StatusCode: tc.statusCode, Body: "test body"}
		classified := c.Classify(httpErr)
		if classified.Reason != tc.reason {
			t.Errorf("status %d: expected reason %s, got %s", tc.statusCode, tc.reason, classified.Reason)
		}
		if classified.Retryable != tc.retryable {
			t.Errorf("status %d: expected retryable=%v, got %v", tc.statusCode, tc.retryable, classified.Retryable)
		}
	}
}

func TestClassifier_HTTPError_Wrapped(t *testing.T) {
	c := NewErrorClassifier()

	inner := &HTTPError{StatusCode: 429, Body: "rate limited"}
	wrapped := fmt.Errorf("provider call failed: %w", inner)

	classified := c.Classify(wrapped)
	if classified.Reason != ReasonRateLimit {
		t.Errorf("expected ReasonRateLimit for wrapped HTTPError, got %s", classified.Reason)
	}
	if !classified.Retryable {
		t.Error("expected wrapped 429 to be retryable")
	}
}

func TestClassifier_HTTPErrorFallbackToPattern(t *testing.T) {
	c := NewErrorClassifier()

	httpErr := &HTTPError{StatusCode: 418, Body: "rate limit exceeded"}
	classified := c.Classify(httpErr)

	if classified.Reason != ReasonRateLimit {
		t.Errorf("expected pattern-based ReasonRateLimit for HTTP 418 with rate limit body, got %s", classified.Reason)
	}
}

func TestClassifier_IsRetryable(t *testing.T) {
	c := NewErrorClassifier()

	if !c.IsRetryable(fmt.Errorf("rate limit exceeded")) {
		t.Error("rate limit should be retryable")
	}
	if c.IsRetryable(fmt.Errorf("invalid api key")) {
		t.Error("auth error should not be retryable")
	}
	if c.IsRetryable(fmt.Errorf("billing issue")) {
		t.Error("billing error should not be retryable")
	}
	if !c.IsRetryable(fmt.Errorf("connection refused")) {
		t.Error("network error should be retryable")
	}
}

func TestClassifier_Cooldown(t *testing.T) {
	c := NewErrorClassifier()

	if cd := c.Cooldown(fmt.Errorf("rate limit exceeded")); cd != 60*time.Second {
		t.Errorf("expected 60s cooldown for rate limit, got %v", cd)
	}
	if cd := c.Cooldown(fmt.Errorf("invalid api key")); cd != 0 {
		t.Errorf("expected 0 cooldown for auth, got %v", cd)
	}
	if cd := c.Cooldown(fmt.Errorf("billing issue")); cd != 24*time.Hour {
		t.Errorf("expected 24h cooldown for billing, got %v", cd)
	}
	if cd := c.Cooldown(fmt.Errorf("something random")); cd != 5*time.Second {
		t.Errorf("expected 5s cooldown for unknown, got %v", cd)
	}
}

func TestClassifier_RetryableFunc(t *testing.T) {
	c := NewErrorClassifier()
	fn := c.RetryableFunc()

	if fn == nil {
		t.Fatal("RetryableFunc returned nil")
	}

	if !fn(fmt.Errorf("rate limit exceeded")) {
		t.Error("RetryableFunc: rate limit should be retryable")
	}
	if fn(fmt.Errorf("invalid api key")) {
		t.Error("RetryableFunc: auth error should not be retryable")
	}
	if !fn(fmt.Errorf("unknown gibberish")) {
		t.Error("RetryableFunc: unknown should be retryable (safe default)")
	}
}

func TestClassifier_CaseInsensitive(t *testing.T) {
	c := NewErrorClassifier()

	classified := c.Classify(fmt.Errorf("RATE LIMIT EXCEEDED"))
	if classified.Reason != ReasonRateLimit {
		t.Errorf("expected case-insensitive match, got %s", classified.Reason)
	}

	classified = c.Classify(fmt.Errorf("Invalid API Key"))
	if classified.Reason != ReasonAuth {
		t.Errorf("expected case-insensitive match, got %s", classified.Reason)
	}
}

func TestClassifier_PatternCount(t *testing.T) {
	_ = NewErrorClassifier()

	if len(classifierPatterns) < 40 {
		t.Errorf("expected at least 40 patterns, got %d", len(classifierPatterns))
	}
}

func TestClassifier_OriginalErrorPreserved(t *testing.T) {
	c := NewErrorClassifier()

	orig := errors.New("something failed")
	classified := c.Classify(orig)

	if classified.Original != orig {
		t.Error("original error not preserved in ClassifiedError")
	}
}

func TestClassifier_HTTPError_Preserved(t *testing.T) {
	c := NewErrorClassifier()

	orig := &HTTPError{StatusCode: 429, Body: "slow down"}
	classified := c.Classify(orig)

	if classified.Original != orig {
		t.Error("original HTTPError not preserved in ClassifiedError")
	}
}

func TestClassifier_SentinelErrors(t *testing.T) {
	c := NewErrorClassifier()

	classified := c.Classify(ErrModelNotFound)
	if classified.Reason != ReasonModelNotFound {
		t.Errorf("expected ReasonModelNotFound for sentinel, got %s", classified.Reason)
	}

	classified = c.Classify(ErrRateLimited)
	if classified.Reason != ReasonRateLimit {
		t.Errorf("expected ReasonRateLimit for sentinel, got %s", classified.Reason)
	}

	classified = c.Classify(ErrContextLength)
	if classified.Reason != ReasonContextOverflow {
		t.Errorf("expected ReasonContextOverflow for sentinel, got %s", classified.Reason)
	}

	classified = c.Classify(ErrAuthFailed)
	if classified.Reason != ReasonAuth {
		t.Errorf("expected ReasonAuth for sentinel, got %s", classified.Reason)
	}
}

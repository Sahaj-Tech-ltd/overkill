package providers

import (
	"errors"
	"regexp"
	"time"
)

type FailoverReason string

const (
	ReasonAuth            FailoverReason = "auth"
	ReasonRateLimit       FailoverReason = "rate_limit"
	ReasonBilling         FailoverReason = "billing"
	ReasonNetwork         FailoverReason = "network"
	ReasonTimeout         FailoverReason = "timeout"
	ReasonFormat          FailoverReason = "format"
	ReasonContextOverflow FailoverReason = "context_overflow"
	ReasonOverloaded      FailoverReason = "overloaded"
	ReasonModelNotFound   FailoverReason = "model_not_found"
	ReasonUnknown         FailoverReason = "unknown"
)

type ClassifiedError struct {
	Original  error
	Reason    FailoverReason
	Retryable bool
	Cooldown  time.Duration
}

type errorPattern struct {
	regex     *regexp.Regexp
	reason    FailoverReason
	retryable bool
	cooldown  time.Duration
}

type ErrorClassifier struct {
	patterns []errorPattern
}

func NewErrorClassifier() *ErrorClassifier {
	c := &ErrorClassifier{}

	c.patterns = []errorPattern{
		{regexp.MustCompile(`(?i)\b401\b`), ReasonAuth, false, 0},
		{regexp.MustCompile(`(?i)\b403\b`), ReasonAuth, false, 0},
		{regexp.MustCompile(`(?i)invalid\s+api\s+key`), ReasonAuth, false, 0},
		{regexp.MustCompile(`(?i)\bauthentication\b`), ReasonAuth, false, 0},
		{regexp.MustCompile(`(?i)\bunauthorized\b`), ReasonAuth, false, 0},
		{regexp.MustCompile(`(?i)\bforbidden\b`), ReasonAuth, false, 0},
		{regexp.MustCompile(`(?i)access\s+denied`), ReasonAuth, false, 0},
		{regexp.MustCompile(`(?i)\b429\b`), ReasonRateLimit, true, 60 * time.Second},
		{regexp.MustCompile(`(?i)rate\s+limit`), ReasonRateLimit, true, 60 * time.Second},
		{regexp.MustCompile(`(?i)too\s+many\s+requests`), ReasonRateLimit, true, 60 * time.Second},
		{regexp.MustCompile(`(?i)quota\s+exceeded`), ReasonRateLimit, true, 60 * time.Second},
		{regexp.MustCompile(`(?i)requests?\s+per\s+(minute|second)`), ReasonRateLimit, true, 60 * time.Second},
		{regexp.MustCompile(`(?i)\b402\b`), ReasonBilling, false, 24 * time.Hour},
		{regexp.MustCompile(`(?i)\bbilling\b`), ReasonBilling, false, 24 * time.Hour},
		{regexp.MustCompile(`(?i)\bpayment\b`), ReasonBilling, false, 24 * time.Hour},
		{regexp.MustCompile(`(?i)insufficient\s+funds`), ReasonBilling, false, 24 * time.Hour},
		{regexp.MustCompile(`(?i)\bcredit`), ReasonBilling, false, 24 * time.Hour},
		{regexp.MustCompile(`(?i)\bsubscription\b`), ReasonBilling, false, 24 * time.Hour},
		{regexp.MustCompile(`(?i)plan\s+limit`), ReasonBilling, false, 24 * time.Hour},
		{regexp.MustCompile(`(?i)connection\s+refused`), ReasonNetwork, true, 10 * time.Second},
		{regexp.MustCompile(`(?i)\bdns\b`), ReasonNetwork, true, 10 * time.Second},
		{regexp.MustCompile(`(?i)no\s+such\s+host`), ReasonNetwork, true, 10 * time.Second},
		{regexp.MustCompile(`(?i)\bnetwork\b`), ReasonNetwork, true, 10 * time.Second},
		{regexp.MustCompile(`(?i)ECONNREFUSED`), ReasonNetwork, true, 10 * time.Second},
		{regexp.MustCompile(`(?i)ECONNRESET`), ReasonNetwork, true, 10 * time.Second},
		{regexp.MustCompile(`(?i)EPIPE\b`), ReasonNetwork, true, 10 * time.Second},
		{regexp.MustCompile(`(?i)connection\s+reset`), ReasonNetwork, true, 10 * time.Second},
		{regexp.MustCompile(`(?i)context\s+deadline`), ReasonTimeout, true, 30 * time.Second},
		{regexp.MustCompile(`(?i)context\s+canceled`), ReasonTimeout, true, 30 * time.Second},
		{regexp.MustCompile(`(?i)i/o\s+timeout`), ReasonTimeout, true, 30 * time.Second},
		{regexp.MustCompile(`(?i)read\s+timeout`), ReasonTimeout, true, 30 * time.Second},
		{regexp.MustCompile(`(?i)deadline\s+exceeded`), ReasonTimeout, true, 30 * time.Second},
		{regexp.MustCompile(`(?i)\bjson\b`), ReasonFormat, false, 0},
		{regexp.MustCompile(`(?i)\bunmarshal\b`), ReasonFormat, false, 0},
		{regexp.MustCompile(`(?i)invalid\s+response`), ReasonFormat, false, 0},
		{regexp.MustCompile(`(?i)unexpected\s+end`), ReasonFormat, false, 0},
		{regexp.MustCompile(`(?i)\bdecode\b`), ReasonFormat, false, 0},
		{regexp.MustCompile(`(?i)syntax\s+error`), ReasonFormat, false, 0},
		{regexp.MustCompile(`(?i)context\s+length`), ReasonContextOverflow, false, 0},
		{regexp.MustCompile(`(?i)maximum\s+context`), ReasonContextOverflow, false, 0},
		{regexp.MustCompile(`(?i)too\s+many\s+tokens`), ReasonContextOverflow, false, 0},
		{regexp.MustCompile(`(?i)token\s+limit`), ReasonContextOverflow, false, 0},
		{regexp.MustCompile(`(?i)input\s+is\s+too\s+long`), ReasonContextOverflow, false, 0},
		{regexp.MustCompile(`(?i)max_tokens`), ReasonContextOverflow, false, 0},
		{regexp.MustCompile(`(?i)\b500\b`), ReasonOverloaded, true, 30 * time.Second},
		{regexp.MustCompile(`(?i)\b502\b`), ReasonOverloaded, true, 30 * time.Second},
		{regexp.MustCompile(`(?i)\b503\b`), ReasonOverloaded, true, 30 * time.Second},
		{regexp.MustCompile(`(?i)\b529\b`), ReasonOverloaded, true, 30 * time.Second},
		{regexp.MustCompile(`(?i)\boverloaded\b`), ReasonOverloaded, true, 30 * time.Second},
		{regexp.MustCompile(`(?i)internal\s+server\s+error`), ReasonOverloaded, true, 30 * time.Second},
		{regexp.MustCompile(`(?i)bad\s+gateway`), ReasonOverloaded, true, 30 * time.Second},
		{regexp.MustCompile(`(?i)service\s+unavailable`), ReasonOverloaded, true, 30 * time.Second},
		{regexp.MustCompile(`(?i)model\s+not\s+found`), ReasonModelNotFound, false, 0},
		{regexp.MustCompile(`(?i)model\s+not\s+available`), ReasonModelNotFound, false, 0},
		{regexp.MustCompile(`(?i)does\s+not\s+exist`), ReasonModelNotFound, false, 0},
		{regexp.MustCompile(`(?i)model\s+.*\bnot\b`), ReasonModelNotFound, false, 0},
		{regexp.MustCompile(`(?i)fork/exec`), ReasonUnknown, true, 5 * time.Second},
		{regexp.MustCompile(`(?i)signal:\s+killed`), ReasonUnknown, true, 5 * time.Second},
		{regexp.MustCompile(`(?i)signal:\s+aborted`), ReasonUnknown, true, 5 * time.Second},
	}

	return c
}

func (c *ErrorClassifier) Classify(err error) ClassifiedError {
	if err == nil {
		return ClassifiedError{
			Original:  err,
			Reason:    ReasonUnknown,
			Retryable: false,
			Cooldown:  0,
		}
	}

	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		reason, retryable, cooldown := c.classifyHTTPStatus(httpErr.StatusCode)
		if reason != ReasonUnknown {
			return ClassifiedError{
				Original:  err,
				Reason:    reason,
				Retryable: retryable,
				Cooldown:  cooldown,
			}
		}
	}

	msg := err.Error()
	for _, p := range c.patterns {
		if p.regex.MatchString(msg) {
			return ClassifiedError{
				Original:  err,
				Reason:    p.reason,
				Retryable: p.retryable,
				Cooldown:  p.cooldown,
			}
		}
	}

	return ClassifiedError{
		Original:  err,
		Reason:    ReasonUnknown,
		Retryable: true,
		Cooldown:  5 * time.Second,
	}
}

func (c *ErrorClassifier) classifyHTTPStatus(code int) (FailoverReason, bool, time.Duration) {
	switch {
	case code == 401 || code == 403:
		return ReasonAuth, false, 0
	case code == 402:
		return ReasonBilling, false, 24 * time.Hour
	case code == 429:
		return ReasonRateLimit, true, 60 * time.Second
	case code == 500 || code == 502 || code == 503 || code == 529:
		return ReasonOverloaded, true, 30 * time.Second
	case code == 408:
		return ReasonTimeout, true, 30 * time.Second
	case code == 413:
		return ReasonContextOverflow, false, 0
	case code == 404:
		return ReasonModelNotFound, false, 0
	default:
		return ReasonUnknown, false, 0
	}
}

func (c *ErrorClassifier) IsRetryable(err error) bool {
	return c.Classify(err).Retryable
}

func (c *ErrorClassifier) Cooldown(err error) time.Duration {
	return c.Classify(err).Cooldown
}

func (c *ErrorClassifier) RetryableFunc() func(error) bool {
	return func(err error) bool { return c.IsRetryable(err) }
}

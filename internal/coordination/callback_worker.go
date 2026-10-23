package coordination

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
)

// CallbackWorker only delivers durable events. It never reserves or executes
// agent work. HTTP acknowledgement and a DB commit cannot be atomic: receivers
// must deduplicate X-Overkill-Event-ID, which is stable through every retry.
type CallbackWorker struct {
	Store        *Store
	Destinations map[string]string
	// Transport is an explicit application/test injection. Production leaves nil
	// to enforce public pinned DNS, HTTPS verification and no proxy/redirects.
	Transport      http.RoundTripper
	PollInterval   time.Duration
	AttemptTimeout time.Duration
	MaxAttempts    int
	OnError        func(error)
}

// CallbackSignature signs version, stable event ID, sequence, attempt timestamp
// and exact body bytes. The secret is hex encoded in the one-time setup result.
func CallbackSignature(secret []byte, id string, sequence int64, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	fmt.Fprintf(mac, "v1\n%s\n%d\n%s\n", id, sequence, timestamp)
	_, _ = mac.Write(body)
	return "v1=" + hex.EncodeToString(mac.Sum(nil))
}
func (w *CallbackWorker) Run(ctx context.Context) error {
	if w.Store == nil {
		return fmt.Errorf("callbacks: store required")
	}
	if err := ValidateCallbackDestinations(w.Destinations); err != nil {
		return err
	}
	interval := w.PollInterval
	if interval == 0 {
		interval = 250 * time.Millisecond
	}
	timeout := w.AttemptTimeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	attempts := w.MaxAttempts
	if attempts == 0 {
		attempts = 6
	}
	if interval < 10*time.Millisecond || interval > time.Minute || timeout < 10*time.Millisecond || timeout > 15*time.Second || attempts < 1 || attempts > 10 {
		return fmt.Errorf("callbacks: invalid worker bounds")
	}
	transport := w.Transport
	if transport == nil {
		transport = newCallbackTransport()
	}
	client := &http.Client{Transport: transport, Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	owner := uuid.NewString()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		claim, err := w.Store.claimCallback(ctx, owner)
		if err != nil {
			if ctx.Err() == nil && w.OnError != nil {
				w.OnError(fmt.Errorf("callbacks: durable claim failed"))
			}
		} else if claim != nil {
			status, failure, interrupted := 0, "attempts_exhausted", false
			if claim.attempts <= attempts {
				status, failure, interrupted = w.attempt(ctx, client, claim, timeout)
			}
			dead := claim.attempts >= attempts || (status >= 400 && status < 500 && status != 408 && status != 429) || failure == "destination_unavailable"
			finalCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			err = w.Store.finishCallback(finalCtx, claim, status, failure, dead, interrupted)
			cancel()
			if err != nil && !errors.Is(err, ErrLeaseLost) && w.OnError != nil {
				w.OnError(fmt.Errorf("callbacks: durable acknowledgement failed"))
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
func (w *CallbackWorker) attempt(parent context.Context, client *http.Client, c *callbackClaim, timeout time.Duration) (int, string, bool) {
	destination, ok := w.Destinations[c.destination]
	if !ok {
		return 0, "destination_unavailable", false
	}
	// Check immediately before outbound I/O as well as during the attempt.
	// A request already accepted by the receiver cannot be recalled.
	owned, err := w.Store.callbackOwned(parent, c)
	if err != nil || !owned {
		return 0, "ownership_lost", parent.Err() != nil
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	// Revocation and shutdown own the in-flight attempt, not the request that
	// created the subscription. A single bounded monitor is joined before return.
	monitorDone := make(chan struct{})
	go func() {
		defer close(monitorDone)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				owned, err := w.Store.callbackOwned(ctx, c)
				if err != nil || !owned {
					cancel()
					return
				}
			}
		}
	}()
	defer func() { cancel(); <-monitorDone }()
	id := fmt.Sprintf("%s:%d", c.id, c.sequence)
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, destination, bytes.NewReader(c.body))
	if err != nil {
		return 0, "request_invalid", false
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Overkill-Event-ID", id)
	req.Header.Set("X-Overkill-Sequence", strconv.FormatInt(c.sequence, 10))
	req.Header.Set("X-Overkill-Timestamp", timestamp)
	req.Header.Set("X-Overkill-Signature", CallbackSignature(c.secret, id, c.sequence, timestamp, c.body))
	response, err := client.Do(req)
	if err != nil {
		if parent.Err() != nil {
			return 0, "interrupted", true
		}
		if ctx.Err() != nil {
			return 0, "cancelled_or_timed_out", false
		}
		return 0, "transport_failed", false
	}
	defer response.Body.Close()
	// Response bodies are diagnostic-only and never persisted or reflected. The
	// acknowledgement is the HTTP status, bounded independently of response size.
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if ctx.Err() != nil {
		return 0, "cancelled_or_timed_out", parent.Err() != nil
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return response.StatusCode, "", false
	}
	return response.StatusCode, "http_status", false
}

package sinks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/events"
)

// WebhookSink POSTs CompletionEvent JSON to a configurable URL with a Bearer
// token. It retries once on 5xx responses. Suited for Slack incoming webhooks
// and custom notification systems.
type WebhookSink struct {
	url    string
	token  string
	client *http.Client
}

// NewWebhookSink returns a WebhookSink that POSTs to url authenticated with
// token. Pass an empty token to omit the Authorization header.
func NewWebhookSink(url, token string) *WebhookSink {
	return &WebhookSink{
		url:   url,
		token: token,
		client: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// Name implements events.Sink.
func (s *WebhookSink) Name() string { return "webhook" }

// Send marshals evt to JSON and POSTs it to the configured URL. It retries
// once on a 5xx response, but never on 4xx (client errors are not retryable).
func (s *WebhookSink) Send(ctx context.Context, evt events.CompletionEvent) error {
	body, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("webhook sink: marshal: %w", err)
	}

	status, err := s.post(ctx, body)
	if err != nil {
		return fmt.Errorf("webhook sink: %w", err)
	}
	if status >= 500 {
		// Retry once on server-side errors.
		status, err = s.post(ctx, body)
		if err != nil {
			return fmt.Errorf("webhook sink: retry: %w", err)
		}
		if status >= 400 {
			return fmt.Errorf("webhook sink: retry HTTP %d", status)
		}
	} else if status >= 400 {
		return fmt.Errorf("webhook sink: HTTP %d (not retried)", status)
	}
	return nil
}

// post performs a single POST and returns the HTTP status code.
func (s *WebhookSink) post(ctx context.Context, body []byte) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url, bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if s.token != "" {
		req.Header.Set("Authorization", "Bearer "+s.token)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("do request: %w", err)
	}
	resp.Body.Close()
	return resp.StatusCode, nil
}

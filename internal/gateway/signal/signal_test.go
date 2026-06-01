package signal

import (
	"context"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/gateway"
)

func TestBot_Name(t *testing.T) {
	b := NewBot("http://localhost:8080", "+1234567890", "auth-token", nil)
	if got := b.Name(); got != "signal" {
		t.Errorf("Name() = %q, want %q", got, "signal")
	}
}

func TestBot_NewBot(t *testing.T) {
	d := &gateway.Dispatcher{}
	b := NewBot("http://localhost:9090", "+1111111111", "sekret", d)

	if b.RestAPIURL != "http://localhost:9090" {
		t.Errorf("RestAPIURL = %q", b.RestAPIURL)
	}
	if b.Account != "+1111111111" {
		t.Errorf("Account = %q", b.Account)
	}
	if b.AuthToken != "sekret" {
		t.Errorf("AuthToken = %q", b.AuthToken)
	}
	if b.Dispatcher != d {
		t.Error("Dispatcher not set")
	}
	if b.PollEvery != 5*time.Second {
		t.Errorf("PollEvery = %v, want 5s", b.PollEvery)
	}
	if b.httpClient == nil {
		t.Error("httpClient is nil")
	}
	if b.Logger == nil {
		t.Error("Logger is nil")
	}
}

func TestBot_RunCancelledContext(t *testing.T) {
	d := &gateway.Dispatcher{}
	b := NewBot("http://localhost:8080", "+1234567890", "token", d)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan error, 1)
	go func() { done <- b.Run(ctx) }()

	select {
	case err := <-done:
		// With cancelled ctx, receive() fails via http.NewRequestWithContext,
		// then Run checks ctx.Err() and returns it immediately.
		if err == nil {
			t.Error("expected non-nil error from cancelled context")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not return within 5s on cancelled context")
	}
}

func TestBot_Healthy_Unreachable(t *testing.T) {
	// Point at a port that won't respond — Healthy() should return false quickly.
	b := NewBot("http://127.0.0.1:19999", "+1234567890", "", nil)
	b.ClientTimeout = 500 * time.Millisecond

	if b.Healthy() {
		t.Error("expected Healthy() to return false when REST API is unreachable")
	}
}

func TestBot_AllowedNumbers(t *testing.T) {
	b := NewBot("http://localhost:8080", "+1234567890", "", nil)
	b.AllowedNumbers = map[string]bool{"+1234567890": true}

	if !b.AllowedNumbers["+1234567890"] {
		t.Error("expected number to be allowed")
	}
	if b.AllowedNumbers["+9999999999"] {
		t.Error("expected number to be not allowed")
	}
}

func TestBot_ClientTimeoutOverride(t *testing.T) {
	b := NewBot("http://localhost:8080", "+1234567890", "", nil)
	b.ClientTimeout = 2 * time.Second
	client := b.getHTTPClient()
	if client.Timeout != 2*time.Second {
		t.Errorf("ClientTimeout override: got %v, want 2s", client.Timeout)
	}
}

package matrix

import (
	"context"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/gateway"
)

func TestBot_Name(t *testing.T) {
	b := NewBot("https://matrix.example.com", "@bot:example.com", "token", "", nil)
	if got := b.Name(); got != "matrix" {
		t.Errorf("Name() = %q, want %q", got, "matrix")
	}
}

func TestBot_NewBot(t *testing.T) {
	d := &gateway.Dispatcher{}
	b := NewBot("https://homeserver.example.com", "@user:homeserver", "s3cret", "pass", d)

	if b.HomeserverURL != "https://homeserver.example.com" {
		t.Errorf("HomeserverURL = %q", b.HomeserverURL)
	}
	if b.UserID != "@user:homeserver" {
		t.Errorf("UserID = %q", b.UserID)
	}
	if b.AccessToken != "s3cret" {
		t.Errorf("AccessToken = %q", b.AccessToken)
	}
	if b.Password != "pass" {
		t.Errorf("Password = %q", b.Password)
	}
	if b.Dispatcher != d {
		t.Error("Dispatcher not set")
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
	b := NewBot("https://matrix.example.com", "@bot:example.com", "token", "", d)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	done := make(chan error, 1)
	go func() { done <- b.Run(ctx) }()

	select {
	case err := <-done:
		if err == nil {
			t.Error("expected non-nil error from cancelled context")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not return within 5s on cancelled context")
	}
}

func TestBot_AllowedRooms(t *testing.T) {
	b := NewBot("https://matrix.example.com", "@bot:example.com", "token", "", nil)
	b.AllowedRooms = map[string]bool{"!room1:example.com": true}

	if !b.AllowedRooms["!room1:example.com"] {
		t.Error("expected room to be allowed")
	}
	if b.AllowedRooms["!other:example.com"] {
		t.Error("expected room to be not allowed")
	}
}

func TestBot_Healthy_BeforeRun(t *testing.T) {
	b := NewBot("https://matrix.example.com", "@bot:example.com", "token", "", nil)
	if b.Healthy() {
		t.Error("expected Healthy() to return false before Run")
	}
}

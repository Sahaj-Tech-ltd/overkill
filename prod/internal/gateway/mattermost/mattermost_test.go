package mattermost

import (
	"context"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/gateway"
)

func TestBot_Name(t *testing.T) {
	b := NewBot("https://mattermost.example.com", "bot-token", "myteam", nil)
	if got := b.Name(); got != "mattermost" {
		t.Errorf("Name() = %q, want %q", got, "mattermost")
	}
}

func TestBot_NewBot(t *testing.T) {
	d := &gateway.Dispatcher{}
	b := NewBot("https://mm.example.com/", "tok", "team", d)

	if b.ServerURL != "https://mm.example.com" {
		t.Errorf("ServerURL not trimmed: got %q", b.ServerURL)
	}
	if b.BotToken != "tok" {
		t.Errorf("BotToken = %q", b.BotToken)
	}
	if b.TeamName != "team" {
		t.Errorf("TeamName = %q", b.TeamName)
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
	b := NewBot("https://mm.example.com", "tok", "team", d)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan error, 1)
	go func() { done <- b.Run(ctx) }()

	select {
	case err := <-done:
		if err != context.Canceled {
			t.Errorf("expected context.Canceled, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run() did not return immediately on cancelled context")
	}
}

func TestBot_Healthy_BeforeRun(t *testing.T) {
	b := NewBot("https://mm.example.com", "tok", "team", nil)
	if b.Healthy() {
		t.Error("expected Healthy() to return false before Run")
	}
}

func TestBot_AllowedChannels(t *testing.T) {
	b := NewBot("https://mm.example.com", "tok", "team", nil)
	b.AllowedChannels = map[string]bool{"ch1": true}
	b.AllowedUsers = map[string]bool{"u1": true}

	if !b.AllowedChannels["ch1"] {
		t.Error("expected channel to be allowed")
	}
	if !b.AllowedUsers["u1"] {
		t.Error("expected user to be allowed")
	}
	if b.AllowedChannels["ch2"] {
		t.Error("expected channel to be not allowed")
	}
}

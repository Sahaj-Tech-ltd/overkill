package slack

import (
	"context"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/gateway"
)

func TestBot_Name(t *testing.T) {
	b := NewBot("xoxb-bot-token", "xapp-app-token", nil, nil, nil)
	if got := b.Name(); got != "slack" {
		t.Errorf("Name() = %q, want %q", got, "slack")
	}
}

func TestBot_NewBot(t *testing.T) {
	d := &gateway.Dispatcher{}
	allowedUsers := []string{"U001", "U002"}
	allowedChannels := []string{"C001"}

	b := NewBot("xoxb-foo", "xapp-bar", d, allowedUsers, allowedChannels)

	if b.Client == nil {
		t.Error("Client is nil")
	}
	if b.Dispatcher != d {
		t.Error("Dispatcher not set")
	}
	if len(b.Allowed) != 2 {
		t.Errorf("Allowed users map: got %d entries, want 2", len(b.Allowed))
	}
	if !b.Allowed["U001"] || !b.Allowed["U002"] {
		t.Error("expected U001 and U002 to be allowed")
	}
	if b.Allowed["U999"] {
		t.Error("U999 should not be allowed")
	}
	if len(b.AllowedChannels) != 1 {
		t.Errorf("AllowedChannels map: got %d entries, want 1", len(b.AllowedChannels))
	}
	if !b.AllowedChannels["C001"] {
		t.Error("expected C001 to be allowed")
	}
	if b.Logger == nil {
		t.Error("Logger is nil")
	}
	if b.apiSem == nil || cap(b.apiSem) != 3 {
		t.Errorf("apiSem cap = %d, want 3", cap(b.apiSem))
	}
}

func TestBot_RunCancelledContext(t *testing.T) {
	b := NewBot("xoxb-bot-token", "xapp-app-token", nil, nil, nil)

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
		t.Fatal("Run() did not return within 5s on cancelled context")
	}
}

func TestBot_AllowedMaps_Empty(t *testing.T) {
	// Empty allowlists should result in empty maps (all allowed behaviour).
	b := NewBot("xoxb-token", "xapp-token", nil, nil, nil)

	if len(b.Allowed) != 0 {
		t.Errorf("Allowed map with nil input: got %d entries, want 0", len(b.Allowed))
	}
	if len(b.AllowedChannels) != 0 {
		t.Errorf("AllowedChannels map with nil input: got %d entries, want 0", len(b.AllowedChannels))
	}
}

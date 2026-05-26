package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/automation"
	"github.com/Sahaj-Tech-ltd/overkill/internal/daemon"
)

// startTestSocket spins up a daemon socket bound to a tmp path,
// registers the alarm handlers against a real in-memory-store
// AlarmClock, and returns a Client + cleanup.
func startTestSocket(t *testing.T) (*automation.AlarmClock, *daemon.Client) {
	t.Helper()
	sockPath := filepath.Join(t.TempDir(), "test.sock")
	clock := automation.NewAlarmClockWithStore(
		func(*automation.Alarm) error { return nil },
		automation.NewMemoryAlarmStore(),
	)
	srv := daemon.NewServer(sockPath)
	registerAlarmHandlers(srv, clock)
	if err := srv.Start(); err != nil {
		t.Fatalf("socket start: %v", err)
	}
	t.Cleanup(srv.Stop)
	return clock, daemon.NewClient(sockPath)
}

func TestAlarmRPC_SetRoundtrip(t *testing.T) {
	clock, client := startTestSocket(t)

	gw := &daemonAlarmGateway{client: client}
	alarm := &automation.Alarm{
		ID:     "rpc-test-1",
		Name:   "test",
		FireAt: time.Now().Add(time.Hour),
		Prompt: "test prompt",
	}
	if err := gw.Set(alarm); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Verify the daemon-side clock actually has the alarm.
	all := clock.List()
	if len(all) != 1 || all[0].ID != "rpc-test-1" {
		t.Errorf("daemon clock missing alarm: %+v", all)
	}
}

func TestAlarmRPC_ListRoundtrip(t *testing.T) {
	clock, client := startTestSocket(t)
	// Pre-seed via the daemon-side clock directly.
	_ = clock.Set(&automation.Alarm{
		ID: "one", Name: "first", FireAt: time.Now().Add(time.Hour), Prompt: "p1",
	})
	_ = clock.Set(&automation.Alarm{
		ID: "two", Name: "second", FireAt: time.Now().Add(2 * time.Hour), Prompt: "p2",
	})

	gw := &daemonAlarmGateway{client: client}
	got := gw.List()
	if len(got) != 2 {
		t.Fatalf("want 2 alarms, got %d", len(got))
	}
	// Order is by FireAt asc — first should be "one".
	if got[0].ID != "one" {
		t.Errorf("order: %+v", got)
	}
}

func TestAlarmRPC_CancelRoundtrip(t *testing.T) {
	clock, client := startTestSocket(t)
	_ = clock.Set(&automation.Alarm{
		ID: "to-cancel", Name: "x", FireAt: time.Now().Add(time.Hour), Prompt: "p",
	})

	gw := &daemonAlarmGateway{client: client}
	if !gw.Cancel("to-cancel") {
		t.Error("Cancel returned false for known alarm")
	}

	all := clock.List()
	if !all[0].Cancelled {
		t.Errorf("alarm not cancelled on daemon side: %+v", all[0])
	}
}

func TestAlarmRPC_CancelUnknownReturnsFalse(t *testing.T) {
	_, client := startTestSocket(t)
	gw := &daemonAlarmGateway{client: client}
	if gw.Cancel("does-not-exist") {
		t.Error("expected false for unknown id, got true")
	}
}

func TestAlarmRPC_BadParamsReturnsRPCError(t *testing.T) {
	_, client := startTestSocket(t)
	// Send a malformed alarm payload — alarm.set without ID.
	_, err := client.Call("alarm.set", map[string]string{"name": "incomplete"})
	if err == nil {
		t.Error("expected error for incomplete alarm payload")
	}
}

func TestAlarmRPC_PingHandlerStillWorks(t *testing.T) {
	// Sanity check: registering alarm handlers doesn't blow away the
	// ping handler if the daemon registered both.
	sockPath := filepath.Join(t.TempDir(), "test.sock")
	clock := automation.NewAlarmClock(func(*automation.Alarm) error { return nil })
	srv := daemon.NewServer(sockPath)
	srv.Register("ping", func(ctx context.Context, req daemon.Request) (daemon.Response, error) {
		return daemon.Response{Result: []byte(`{"ok":true}`)}, nil
	})
	registerAlarmHandlers(srv, clock)
	if err := srv.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(srv.Stop)

	client := daemon.NewClient(sockPath)
	if _, err := client.Call("ping", nil); err != nil {
		t.Errorf("ping after alarm reg: %v", err)
	}
}

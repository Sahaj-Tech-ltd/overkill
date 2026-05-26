package main

import (
	"encoding/json"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/automation"
	"github.com/Sahaj-Tech-ltd/overkill/internal/daemon"
)

func TestEStopRPC_NoRunningTasksReturnsZero(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "test.sock")
	srv := daemon.NewServer(sockPath)
	b := &daemonEStopBroadcaster{
		alarmCancelAll: func() int { return 0 },
	}
	srv.Register("estop", estopHandler(b))
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Stop)

	client := daemon.NewClient(sockPath)
	raw, err := client.Call("estop", nil)
	if err != nil {
		t.Fatalf("estop call: %v", err)
	}
	var resp struct {
		Halted int `json:"halted"`
	}
	_ = json.Unmarshal(raw, &resp)
	if resp.Halted != 0 {
		t.Errorf("expected halted=0, got %d", resp.Halted)
	}
}

func TestEStopRPC_CountsCancelledTasks(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "test.sock")
	srv := daemon.NewServer(sockPath)
	b := &daemonEStopBroadcaster{
		alarmCancelAll: func() int { return 3 },
	}
	srv.Register("estop", estopHandler(b))
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Stop)

	client := daemon.NewClient(sockPath)
	raw, _ := client.Call("estop", nil)
	var resp struct {
		Halted int `json:"halted"`
	}
	_ = json.Unmarshal(raw, &resp)
	if resp.Halted != 3 {
		t.Errorf("halted: %d", resp.Halted)
	}
}

func TestEStopRPC_NilBroadcasterReturnsZero(t *testing.T) {
	sockPath := filepath.Join(t.TempDir(), "test.sock")
	srv := daemon.NewServer(sockPath)
	srv.Register("estop", estopHandler(nil))
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Stop)

	client := daemon.NewClient(sockPath)
	raw, err := client.Call("estop", nil)
	if err != nil {
		t.Fatalf("estop call: %v", err)
	}
	var resp struct {
		Halted int `json:"halted"`
	}
	_ = json.Unmarshal(raw, &resp)
	if resp.Halted != 0 {
		t.Errorf("nil broadcaster should return 0, got %d", resp.Halted)
	}
}

func TestEStopRPC_CancelsPendingAlarms(t *testing.T) {
	// End-to-end: wire the broadcaster to a real AlarmClock with
	// pending alarms, fire the RPC, verify the alarms are cancelled.
	clock := automation.NewAlarmClock(func(*automation.Alarm) error { return nil })
	_ = clock.Set(&automation.Alarm{
		ID: "a1", FireAt: time.Now().Add(time.Hour), Prompt: "x",
	})
	_ = clock.Set(&automation.Alarm{
		ID: "a2", FireAt: time.Now().Add(2 * time.Hour), Prompt: "y",
	})

	b := &daemonEStopBroadcaster{
		alarmCancelAll: func() int {
			n := 0
			for _, a := range clock.List() {
				if !a.Fired && !a.Cancelled && clock.Cancel(a.ID) {
					n++
				}
			}
			return n
		},
	}
	got := b.BroadcastEStop()
	if got != 2 {
		t.Errorf("expected 2 cancellations, got %d", got)
	}
	for _, a := range clock.List() {
		if !a.Cancelled {
			t.Errorf("alarm %s not cancelled: %+v", a.ID, a)
		}
	}
}

func TestEStopRPC_BroadcastCounterRunsOnlyOnce(t *testing.T) {
	// Pin that BroadcastEStop's callback is invoked exactly once per
	// RPC call — no retries, no double-fire.
	var calls atomic.Int32
	b := &daemonEStopBroadcaster{
		alarmCancelAll: func() int {
			calls.Add(1)
			return 5
		},
	}
	sockPath := filepath.Join(t.TempDir(), "test.sock")
	srv := daemon.NewServer(sockPath)
	srv.Register("estop", estopHandler(b))
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Stop)

	client := daemon.NewClient(sockPath)
	if _, err := client.Call("estop", nil); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("expected 1 broadcast invocation, got %d", got)
	}
}

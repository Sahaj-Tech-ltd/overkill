package main

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/automation"
	"github.com/Sahaj-Tech-ltd/overkill/internal/daemon"
)

func startTestFlowSocket(t *testing.T) (agent.FlowStore, *automation.AlarmClock, *daemon.Client) {
	t.Helper()
	sockPath := filepath.Join(t.TempDir(), "flow.sock")
	store := agent.NewMemoryFlowStore()
	clock := automation.NewAlarmClockWithStore(
		func(*automation.Alarm) error { return nil },
		automation.NewMemoryAlarmStore(),
	)
	srv := daemon.NewServer(sockPath)
	registerAlarmHandlers(srv, clock)
	registerFlowHandlers(srv, store, clock)
	if err := srv.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(srv.Stop)
	return store, clock, daemon.NewClient(sockPath)
}

func TestFlowRPC_CheckpointPersists(t *testing.T) {
	store, _, client := startTestFlowSocket(t)
	sink := &daemonFlowSink{client: client}

	state := &agent.FlowState{
		ID:        "flow-abc",
		SessionID: "sess",
		UserInput: "do the thing",
		Model:     "m",
		Step:      50,
		Reason:    "exceeded budget",
		CreatedAt: time.Now().UTC(),
	}
	if err := sink.Save(state); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := store.Load("flow-abc")
	if err != nil {
		t.Fatal(err)
	}
	if loaded == nil {
		t.Fatal("daemon-side store missing the flow record")
	}
	if loaded.Reason != "exceeded budget" {
		t.Errorf("reason lost: %q", loaded.Reason)
	}
}

func TestFlowRPC_SchedulesResumeAlarm(t *testing.T) {
	_, clock, client := startTestFlowSocket(t)
	sink := &daemonFlowSink{client: client}

	state := &agent.FlowState{ID: "flow-xyz", UserInput: "task", CreatedAt: time.Now().UTC()}
	if err := sink.Save(state); err != nil {
		t.Fatal(err)
	}

	alarms := clock.List()
	if len(alarms) != 1 {
		t.Fatalf("want 1 alarm scheduled, got %d", len(alarms))
	}
	got := alarms[0]
	if got.ID != "resume-flow-xyz" {
		t.Errorf("alarm ID: %s", got.ID)
	}
	if agent.ExtractFlowID(got.Prompt) != "flow-xyz" {
		t.Errorf("alarm prompt not a resume prompt: %q", got.Prompt)
	}
	// Default 5m schedule.
	delta := time.Until(got.FireAt)
	if delta < 4*time.Minute || delta > 6*time.Minute {
		t.Errorf("schedule not ~5m: %v", delta)
	}
}

func TestFlowRPC_RescheduleReplacesPriorAlarm(t *testing.T) {
	_, clock, client := startTestFlowSocket(t)
	sink := &daemonFlowSink{client: client}

	state := &agent.FlowState{ID: "f", UserInput: "x", CreatedAt: time.Now().UTC()}
	if err := sink.Save(state); err != nil {
		t.Fatal(err)
	}
	// Re-checkpoint same flow (e.g. resumed run timed out again).
	if err := sink.Save(state); err != nil {
		t.Fatal(err)
	}
	alarms := clock.List()
	pending := 0
	for _, a := range alarms {
		if !a.Cancelled && !a.Fired {
			pending++
		}
	}
	if pending != 1 {
		t.Errorf("expected 1 pending alarm after re-checkpoint, got %d", pending)
	}
}

func TestFlowRPC_RejectsMissingState(t *testing.T) {
	_, _, client := startTestFlowSocket(t)
	// nil state field.
	_, err := client.Call("flow.checkpoint", map[string]any{})
	if err == nil {
		t.Error("expected error for missing state")
	}
}

func TestFlowRPC_RejectsEmptyID(t *testing.T) {
	_, _, client := startTestFlowSocket(t)
	_, err := client.Call("flow.checkpoint", flowCheckpointParams{State: &agent.FlowState{}})
	if err == nil {
		t.Error("expected error for empty state.id")
	}
}

func TestScheduleResumeAlarm_CustomDuration(t *testing.T) {
	clock := automation.NewAlarmClock(func(*automation.Alarm) error { return nil })
	scheduleResumeAlarm(clock, "f1", "30s")

	alarms := clock.List()
	if len(alarms) != 1 {
		t.Fatalf("want 1, got %d", len(alarms))
	}
	delta := time.Until(alarms[0].FireAt)
	if delta < 25*time.Second || delta > 35*time.Second {
		t.Errorf("custom duration ignored: %v", delta)
	}
}

func TestScheduleResumeAlarm_InvalidDurationFallsBack(t *testing.T) {
	clock := automation.NewAlarmClock(func(*automation.Alarm) error { return nil })
	scheduleResumeAlarm(clock, "f1", "not a duration")

	alarms := clock.List()
	if len(alarms) != 1 {
		t.Fatalf("want 1, got %d", len(alarms))
	}
	delta := time.Until(alarms[0].FireAt)
	// Should fall back to default 5m.
	if delta < 4*time.Minute || delta > 6*time.Minute {
		t.Errorf("invalid duration should fall back to 5m, got %v", delta)
	}
}

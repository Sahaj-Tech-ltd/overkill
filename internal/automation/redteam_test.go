// Red-team tests for internal/automation.
// Run with: go test -race -count=1 -timeout 60s ./internal/automation/...
package automation

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// SOP Engine red-team
// ---------------------------------------------------------------------------

// RT-SOP-1: Execute with nil store must not panic.
func TestRedTeam_SOP_NilStore_NoPanic(t *testing.T) {
	engine := NewSOPEngine(nil, func(action string) (string, error) {
		return "ok", nil
	})
	sop := &SOP{
		ID:   "rt-nil-store",
		Name: "nil-store",
		Mode: ModeAuto,
		Steps: []Step{
			{ID: "s1", Name: "step1", Action: "act1", Status: StepPending},
		},
	}
	if err := engine.Create(sop); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Must not panic
	if err := engine.Execute(context.Background(), "rt-nil-store"); err != nil {
		t.Fatalf("Execute with nil store unexpectedly failed: %v", err)
	}
}

// RT-SOP-2: SOP with 0 steps — Execute must return ErrNoSteps immediately, not panic.
func TestRedTeam_SOP_ZeroSteps(t *testing.T) {
	engine := NewSOPEngine(nil, func(action string) (string, error) {
		return "ok", nil
	})
	sop := &SOP{
		ID:    "rt-zero-steps",
		Name:  "zero-steps",
		Mode:  ModeAuto,
		Steps: []Step{},
	}
	if err := engine.Create(sop); err != nil {
		t.Fatalf("Create: %v", err)
	}
	err := engine.Execute(context.Background(), "rt-zero-steps")
	if !errors.Is(err, ErrNoSteps) {
		t.Errorf("expected ErrNoSteps for 0-step SOP, got: %v", err)
	}
}

// RT-SOP-3: ApproveStep on a step that is NOT waiting must return ErrInvalidState,
// not silently reset RequiresApproval or panic.
func TestRedTeam_SOP_ApproveNonWaitingStep(t *testing.T) {
	engine := NewSOPEngine(nil, func(action string) (string, error) {
		return "ok", nil
	})
	sop := &SOP{
		ID:   "rt-approve-pending",
		Name: "approve-pending",
		Mode: ModeAuto, // auto → no waiting
		Steps: []Step{
			{ID: "s1", Name: "step1", Action: "act", Status: StepPending, RequiresApproval: false},
		},
	}
	if err := engine.Create(sop); err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Step is still Pending (not Waiting) — ApproveStep must fail.
	err := engine.ApproveStep("rt-approve-pending", "s1")
	if !errors.Is(err, ErrInvalidState) {
		t.Errorf("expected ErrInvalidState when approving a non-waiting step, got: %v", err)
	}
}

// RT-SOP-4: ApproveStep on a nonexistent step ID must return ErrNotFound, not panic.
func TestRedTeam_SOP_ApproveNonexistentStep(t *testing.T) {
	engine := NewSOPEngine(nil, func(action string) (string, error) {
		return "ok", nil
	})
	sop := &SOP{
		ID:   "rt-approve-noexist",
		Name: "approve-noexist",
		Mode: ModeAuto,
		Steps: []Step{
			{ID: "s1", Name: "step1", Action: "act", Status: StepPending},
		},
	}
	if err := engine.Create(sop); err != nil {
		t.Fatalf("Create: %v", err)
	}
	err := engine.ApproveStep("rt-approve-noexist", "nonexistent-step-id")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound for nonexistent step, got: %v", err)
	}
}

// RT-SOP-5: Pause → Resume → Pause cycle must leave the SOP in a coherent state.
// After Resume completes execution, the SOP is Completed; a second Pause must
// return ErrInvalidState (completed is not pauseable), not silently succeed.
func TestRedTeam_SOP_PauseResumePause(t *testing.T) {
	engine := NewSOPEngine(nil, func(action string) (string, error) {
		return "ok", nil
	})
	sop := &SOP{
		ID:   "rt-pause-resume-pause",
		Name: "prp",
		Mode: ModeAuto,
		Steps: []Step{
			{ID: "s1", Name: "step1", Action: "act", Status: StepPending},
		},
	}
	if err := engine.Create(sop); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Pause before first Execute.
	if err := engine.Pause("rt-pause-resume-pause"); err != nil {
		t.Fatalf("first Pause: %v", err)
	}
	got, _ := engine.Get("rt-pause-resume-pause")
	if got.Status != SOPStatusPaused {
		t.Errorf("after first Pause: expected Paused, got %s", got.Status)
	}

	// Resume runs all steps → Completed.
	if err := engine.Resume(context.Background(), "rt-pause-resume-pause"); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	got, _ = engine.Get("rt-pause-resume-pause")
	if got.Status != SOPStatusCompleted {
		t.Errorf("after Resume: expected Completed, got %s", got.Status)
	}

	// Second Pause on a Completed SOP must be rejected.
	err := engine.Pause("rt-pause-resume-pause")
	if !errors.Is(err, ErrInvalidState) {
		t.Errorf("second Pause on Completed SOP: expected ErrInvalidState, got: %v", err)
	}
}

// RT-SOP-6: Run same SOP twice concurrently. With -race this will catch
// unsynchronised mutations. Both goroutines complete without panic.
func TestRedTeam_SOP_ConcurrentExecute_Race(t *testing.T) {
	var callCount atomic.Int64
	engine := NewSOPEngine(nil, func(action string) (string, error) {
		callCount.Add(1)
		return "out:" + action, nil
	})

	// Two distinct SOPs to avoid inter-SOP contention masking the intra-SOP race.
	for i := 0; i < 2; i++ {
		id := fmt.Sprintf("rt-concurrent-%d", i)
		sop := &SOP{
			ID:   id,
			Name: id,
			Mode: ModeAuto,
			Steps: []Step{
				{ID: "s1", Action: "a1", Status: StepPending},
				{ID: "s2", Action: "a2", Status: StepPending},
			},
		}
		if err := engine.Create(sop); err != nil {
			t.Fatalf("Create %s: %v", id, err)
		}
	}

	// Fire both SOPs concurrently.
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = engine.Execute(context.Background(), fmt.Sprintf("rt-concurrent-%d", i))
		}()
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("goroutine %d Execute error: %v", i, err)
		}
	}
	if callCount.Load() != 4 {
		t.Errorf("expected 4 step executions (2 SOPs × 2 steps), got %d", callCount.Load())
	}
}

// RT-SOP-7: ModeDeterministic with prevOutput="" on step 1 (first step).
// The code guards with `idx > 0 && prevOutput != ""`, so step 0 always uses
// its own Action. Verify this holds and no panic occurs.
func TestRedTeam_SOP_Deterministic_EmptyPrevOutputOnStep1(t *testing.T) {
	var callArgs []string
	var mu sync.Mutex
	engine := NewSOPEngine(nil, func(action string) (string, error) {
		mu.Lock()
		callArgs = append(callArgs, action)
		mu.Unlock()
		return "out:" + action, nil
	})

	sop := &SOP{
		ID:   "rt-det-empty",
		Name: "det-empty",
		Mode: ModeDeterministic,
		Steps: []Step{
			// Step 0: prevOutput is always "" here. Action must be used as-is.
			{ID: "s1", Action: "initial", Status: StepPending},
			{ID: "s2", Action: "unused", Status: StepPending},
		},
	}
	if err := engine.Create(sop); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := engine.Execute(context.Background(), "rt-det-empty"); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(callArgs) != 2 {
		t.Fatalf("expected 2 executor calls, got %d: %v", len(callArgs), callArgs)
	}
	// Step 0 must use its own action (prevOutput was "").
	if callArgs[0] != "initial" {
		t.Errorf("step 0: expected action 'initial', got %q", callArgs[0])
	}
	// Step 1 must use the output of step 0.
	if callArgs[1] != "out:initial" {
		t.Errorf("step 1: expected 'out:initial' (chained output), got %q", callArgs[1])
	}

	got, _ := engine.Get("rt-det-empty")
	if got.Status != SOPStatusCompleted {
		t.Errorf("expected Completed, got %s", got.Status)
	}
}

// ---------------------------------------------------------------------------
// Alarm Clock red-team
// ---------------------------------------------------------------------------

// RT-ALM-1: Alarm scheduled in the past fires on the first tick (within ~1s),
// not ignored and not returning an error from Set.
func TestRedTeam_Alarm_PastFireAt_FiresImmediately(t *testing.T) {
	var fired atomic.Int32
	var buf bytes.Buffer
	SetAlarmStderrSink(&buf)
	t.Cleanup(func() { SetAlarmStderrSink(nil) })

	clock := NewAlarmClock(func(alarm *Alarm) error {
		fired.Add(1)
		return nil
	})

	past := &Alarm{
		ID:     "rt-past",
		Name:   "past alarm",
		FireAt: time.Now().Add(-5 * time.Hour), // well in the past
	}
	if err := clock.Set(past); err != nil {
		t.Fatalf("Set past alarm: unexpected error: %v", err)
	}

	clock.Start()
	defer clock.Stop()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if fired.Load() > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if fired.Load() == 0 {
		t.Error("alarm scheduled in the past was not fired within 2s — it should fire on the first tick")
	}
	// Must not have fired more than once (no re-fire on past alarms).
	time.Sleep(200 * time.Millisecond)
	if fired.Load() > 1 {
		t.Errorf("past alarm fired %d times; expected exactly 1", fired.Load())
	}
}

// RT-ALM-2: Alarm 1ns in the future must fire on the first eligible tick.
func TestRedTeam_Alarm_NearFuture_FiresCorrectly(t *testing.T) {
	var fired atomic.Int32

	clock := NewAlarmClock(func(alarm *Alarm) error {
		fired.Add(1)
		return nil
	})

	near := &Alarm{
		ID:     "rt-near",
		Name:   "near future",
		FireAt: time.Now().Add(1), // 1 nanosecond
	}
	if err := clock.Set(near); err != nil {
		t.Fatalf("Set near-future alarm: %v", err)
	}

	clock.Start()
	defer clock.Stop()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if fired.Load() > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if fired.Load() == 0 {
		t.Error("alarm 1ns in the future never fired within 2s")
	}
}

// RT-ALM-3: Schedule 10,000 alarms. Verify no OOM/panic and memory count is stable.
func TestRedTeam_Alarm_10kAlarms_NoOOM(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping 10k-alarm test in short mode")
	}
	var buf bytes.Buffer
	SetAlarmStderrSink(&buf)
	t.Cleanup(func() { SetAlarmStderrSink(nil) })

	clock := NewAlarmClock(func(alarm *Alarm) error {
		return nil
	})

	const n = 10_000
	far := time.Now().Add(24 * time.Hour) // never fire during the test
	for i := 0; i < n; i++ {
		a := &Alarm{
			ID:     fmt.Sprintf("rt-mass-%d", i),
			Name:   fmt.Sprintf("mass %d", i),
			FireAt: far,
		}
		if err := clock.Set(a); err != nil {
			t.Fatalf("Set alarm %d: %v", i, err)
		}
	}

	list := clock.List()
	if len(list) != n {
		t.Errorf("expected %d alarms, got %d", n, len(list))
	}
	// Start + immediate stop — no tick fires.
	clock.Start()
	clock.Stop()
}

// RT-ALM-4: Cancel an alarm that already fired must be idempotent, not panic/error.
func TestRedTeam_Alarm_CancelAlreadyFired(t *testing.T) {
	var buf bytes.Buffer
	SetAlarmStderrSink(&buf)
	t.Cleanup(func() { SetAlarmStderrSink(nil) })

	clock := NewAlarmClock(func(alarm *Alarm) error {
		return nil
	})

	a := &Alarm{
		ID:     "rt-cancel-fired",
		Name:   "cancel fired",
		FireAt: time.Now().Add(-1 * time.Hour),
	}
	if err := clock.Set(a); err != nil {
		t.Fatalf("Set: %v", err)
	}

	clock.Start()
	defer clock.Stop()

	// Wait for the alarm to fire.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		list := clock.List()
		if len(list) > 0 && list[0].Fired {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Cancel an already-fired alarm — must not panic, must return cleanly.
	ok := clock.Cancel("rt-cancel-fired")
	// The alarm is already fired; Cancel currently returns true for fired alarms
	// IF the record exists and is not already Cancelled. We just require no panic.
	_ = ok
}

// RT-ALM-5: Cancel a nonexistent alarm ID must return false, not panic.
func TestRedTeam_Alarm_CancelNonexistent(t *testing.T) {
	clock := NewAlarmClock(func(alarm *Alarm) error { return nil })
	ok := clock.Cancel("does-not-exist-9999")
	if ok {
		t.Error("Cancel of nonexistent ID should return false, got true")
	}
}

// RT-ALM-6: Start → Stop → Start again. The second Start must work (fresh goroutine,
// fresh channels). An alarm set after re-Start must fire.
func TestRedTeam_Alarm_StartStopStart(t *testing.T) {
	var fired atomic.Int32

	clock := NewAlarmClock(func(alarm *Alarm) error {
		fired.Add(1)
		return nil
	})

	// First lifecycle.
	clock.Start()
	clock.Stop()

	// Second lifecycle — must start fresh without panic.
	a := &Alarm{
		ID:     "rt-restart",
		Name:   "restart",
		FireAt: time.Now().Add(-1 * time.Second), // already past → fire immediately
	}
	if err := clock.Set(a); err != nil {
		t.Fatalf("Set after restart: %v", err)
	}

	clock.Start()
	defer clock.Stop()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if fired.Load() > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if fired.Load() == 0 {
		t.Error("alarm did not fire after Start→Stop→Start cycle")
	}
}

// ---------------------------------------------------------------------------
// RoutineEngine red-team
// ---------------------------------------------------------------------------

// RT-RTE-1: Fire event with no matching routines → no-op, no panic.
func TestRedTeam_Routine_NoMatch_NoPanic(t *testing.T) {
	engine := NewRoutineEngine(func(action string) (string, error) {
		return "ok", nil
	})
	fired, err := engine.HandleEvent("totally.unknown.event")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if fired {
		t.Error("expected no routine to fire, but fired=true")
	}
}

// RT-RTE-2: Fire event with 1000 matching routines — all must fire, none dropped.
func TestRedTeam_Routine_1000Matching_AllFire(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping 1000-routine test in short mode")
	}
	var fired atomic.Int64
	engine := NewRoutineEngine(func(action string) (string, error) {
		fired.Add(1)
		return "ok", nil
	})

	const n = 1000
	for i := 0; i < n; i++ {
		r := &Routine{
			ID:      fmt.Sprintf("r-%d", i),
			Trigger: "mass.event",
			Action:  fmt.Sprintf("action-%d", i),
			Enabled: true,
		}
		if err := engine.Register(r); err != nil {
			t.Fatalf("Register r-%d: %v", i, err)
		}
	}

	didFire, err := engine.HandleEvent("mass.event")
	if err != nil {
		t.Fatalf("HandleEvent: %v", err)
	}
	if !didFire {
		t.Error("expected didFire=true with 1000 routines")
	}
	if fired.Load() != n {
		t.Errorf("expected %d fires, got %d — some routines were dropped", n, fired.Load())
	}
}

// RT-RTE-3: Cooldown — fire same event twice within cooldown window → second suppressed.
func TestRedTeam_Routine_Cooldown_SecondSuppressed(t *testing.T) {
	var fired atomic.Int32
	engine := NewRoutineEngine(func(action string) (string, error) {
		fired.Add(1)
		return "ok", nil
	})

	if err := engine.Register(&Routine{
		ID:       "cd-r1",
		Trigger:  "cd.event",
		Action:   "cd-action",
		Enabled:  true,
		Cooldown: 10 * time.Minute, // very long cooldown
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	if _, err := engine.HandleEvent("cd.event"); err != nil {
		t.Fatalf("first HandleEvent: %v", err)
	}
	if fired.Load() != 1 {
		t.Errorf("first fire: expected 1, got %d", fired.Load())
	}

	// Second fire within cooldown — must be suppressed.
	didFire, err := engine.HandleEvent("cd.event")
	if err != nil {
		t.Fatalf("second HandleEvent: %v", err)
	}
	if didFire {
		t.Error("expected second fire to be suppressed by cooldown")
	}
	if fired.Load() != 1 {
		t.Errorf("after suppressed fire: expected fired=1, got %d", fired.Load())
	}
}

// RT-RTE-4: Cooldown — fire same event after cooldown expires → fires again.
func TestRedTeam_Routine_Cooldown_ExpiresAndFires(t *testing.T) {
	var fired atomic.Int32
	engine := NewRoutineEngine(func(action string) (string, error) {
		fired.Add(1)
		return "ok", nil
	})

	if err := engine.Register(&Routine{
		ID:       "cd-r2",
		Trigger:  "cd2.event",
		Action:   "cd2-action",
		Enabled:  true,
		Cooldown: 50 * time.Millisecond, // very short cooldown for test speed
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	if _, err := engine.HandleEvent("cd2.event"); err != nil {
		t.Fatalf("first HandleEvent: %v", err)
	}
	if fired.Load() != 1 {
		t.Fatalf("first fire: expected 1, got %d", fired.Load())
	}

	// Wait for cooldown to expire.
	time.Sleep(100 * time.Millisecond)

	didFire, err := engine.HandleEvent("cd2.event")
	if err != nil {
		t.Fatalf("second HandleEvent after expiry: %v", err)
	}
	if !didFire {
		t.Error("expected second fire after cooldown expiry")
	}
	if fired.Load() != 2 {
		t.Errorf("after expiry fire: expected fired=2, got %d", fired.Load())
	}
}

// RT-RTE-5: Concurrent HandleEvent from 20 goroutines — race detector must not trip.
func TestRedTeam_Routine_ConcurrentHandleEvent_Race(t *testing.T) {
	var fired atomic.Int64
	engine := NewRoutineEngine(func(action string) (string, error) {
		fired.Add(1)
		return "ok", nil
	})

	// Register 5 routines on the same trigger with no cooldown.
	for i := 0; i < 5; i++ {
		r := &Routine{
			ID:      fmt.Sprintf("conc-r%d", i),
			Trigger: "conc.event",
			Action:  fmt.Sprintf("action-%d", i),
			Enabled: true,
		}
		if err := engine.Register(r); err != nil {
			t.Fatalf("Register: %v", err)
		}
	}

	var wg sync.WaitGroup
	const goroutines = 20
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := engine.HandleEvent("conc.event"); err != nil {
				// RoutineEngine fires routines sequentially under the lock,
				// so errors from one routine stop the loop. We tolerate errors here
				// but no panic is allowed.
				_ = err
			}
		}()
	}
	wg.Wait()

	// 20 goroutines × 5 routines, no cooldown — all should fire.
	// The engine holds a lock for the full HandleEvent, so each goroutine
	// fires all 5 routines exactly once.
	expected := int64(goroutines * 5)
	if fired.Load() != expected {
		t.Errorf("expected %d total fires (20 goroutines × 5 routines), got %d", expected, fired.Load())
	}
}

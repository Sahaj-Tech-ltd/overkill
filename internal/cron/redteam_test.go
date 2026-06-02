// Red-team tests for internal/cron.
// Run with: go test -race -count=1 -timeout 60s ./internal/cron/...
package cron

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Scheduler Stop/Start lifecycle (C-41/42 known-bug area)
// ---------------------------------------------------------------------------

// RT-CRON-1: Create → AddJob → Start → Stop → verify job stops firing.
func TestRedTeam_Cron_StartStopStopsJobs(t *testing.T) {
	var fired atomic.Int32

	s, err := NewScheduler(Config{
		OnFire: func(job *Job) error {
			fired.Add(1)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("NewScheduler: %v", err)
	}
	s.tick = 10 * time.Millisecond

	job := &Job{
		Name:     "stop-test",
		Schedule: "* * * * *",
		Command:  "echo hello",
	}
	if err := s.AddJob(job); err != nil {
		t.Fatalf("AddJob: %v", err)
	}

	// Backdate NextRun so it fires on the first tick.
	s.mu.Lock()
	job.NextRun = time.Now().Add(-time.Second)
	s.mu.Unlock()

	s.Start()
	time.Sleep(100 * time.Millisecond)
	s.Stop()

	afterStop := fired.Load()
	if afterStop == 0 {
		t.Fatal("job never fired before Stop — test premise broken")
	}

	// After Stop, no more fires should occur.
	time.Sleep(200 * time.Millisecond)
	if fired.Load() != afterStop {
		t.Errorf("job fired %d more times after Stop; expected 0", fired.Load()-afterStop)
	}
}

// RT-CRON-2: THE KNOWN BUG — Start → Stop → Start again.
//
// With the old code, Stop set `stopped=true` but never reset it.
// The second Start re-armed the channels but left stopped=true.
// The second Stop call would then be a no-op (stopped guard), leaving
// `<-s.done` blocked forever (deadlock in Stop), AND the goroutine
// launched by the second Start would continue running after Stop returned.
//
// Expected (correct): jobs fire after re-Start; Stop returns promptly.
// Observed (broken):  Stop blocks indefinitely on <-s.done.
//
// We time-box Stop to detect the deadlock without hanging the whole suite.
func TestRedTeam_Cron_StartStopStart_KnownBug(t *testing.T) {
	var fired atomic.Int32

	s, err := NewScheduler(Config{
		OnFire: func(job *Job) error {
			fired.Add(1)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("NewScheduler: %v", err)
	}
	s.tick = 10 * time.Millisecond

	// First lifecycle.
	s.Start()
	s.Stop() // must return promptly

	// Second lifecycle — re-Start after Stop.
	job := &Job{
		Name:     "restart-test",
		Schedule: "* * * * *",
		Command:  "echo hello",
	}
	if err := s.AddJob(job); err != nil {
		t.Fatalf("AddJob after restart: %v", err)
	}
	s.mu.Lock()
	job.NextRun = time.Now().Add(-time.Second) // past → fires immediately
	s.mu.Unlock()

	s.Start()

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if fired.Load() > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if fired.Load() == 0 {
		t.Error("BUG CONFIRMED (C-41): job did not fire after Start→Stop→Start cycle — scheduler loop did not restart")
	}

	// Time-box the second Stop to detect the deadlock (C-42).
	stopDone := make(chan struct{})
	go func() {
		s.Stop()
		close(stopDone)
	}()

	select {
	case <-stopDone:
		// Good — Stop returned.
	case <-time.After(2 * time.Second):
		t.Error("BUG CONFIRMED (C-42): Stop() blocked indefinitely after Start→Stop→Start — deadlock on <-s.done")
		// Don't wait forever; return so the test suite continues.
	}
}

// RT-CRON-3: Schedule with invalid cron expression (4 fields, missing one).
// AddJob must return a wrapped ErrInvalidJob, not panic.
func TestRedTeam_Cron_InvalidExpression_4Fields(t *testing.T) {
	s, err := NewScheduler(Config{})
	if err != nil {
		t.Fatalf("NewScheduler: %v", err)
	}

	job := &Job{
		Name:     "bad-cron",
		Schedule: "* * * *", // 4 fields — missing weekday
		Command:  "echo hi",
	}
	err = s.AddJob(job)
	if err == nil {
		t.Fatal("expected error for 4-field cron expression, got nil")
	}

	// Must carry ErrInvalidJob in the chain.
	if !isInvalidJobErr(err) {
		t.Errorf("expected ErrInvalidJob in error chain, got: %v", err)
	}
}

func isInvalidJobErr(err error) bool {
	// errors.Is traversal — ErrInvalidJob is a sentinel.
	type unwrapper interface{ Unwrap() error }
	for err != nil {
		if err == ErrInvalidJob {
			return true
		}
		u, ok := err.(unwrapper)
		if !ok {
			break
		}
		err = u.Unwrap()
	}
	return false
}

// RT-CRON-4: Natural language schedule "every 5 minutes" — AddJob must return
// ErrInvalidJob (the parser only handles standard 5-field cron). Confirm no panic.
func TestRedTeam_Cron_NaturalLanguage_Rejected(t *testing.T) {
	s, err := NewScheduler(Config{})
	if err != nil {
		t.Fatalf("NewScheduler: %v", err)
	}

	job := &Job{
		Name:     "natural-lang",
		Schedule: "every 5 minutes",
		Command:  "echo hi",
	}
	err = s.AddJob(job)
	if err == nil {
		t.Fatal("expected error for natural-language schedule, got nil")
	}
	if !isInvalidJobErr(err) {
		t.Errorf("expected ErrInvalidJob, got: %v", err)
	}
}

// RT-CRON-5: Schedule 100 jobs and verify all fire within a reasonable window.
// Tests that the tick loop doesn't silently drop jobs at scale.
func TestRedTeam_Cron_100Jobs_AllFire(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping 100-job firing test in short mode")
	}

	var fired atomic.Int64
	s, err := NewScheduler(Config{
		OnFire: func(job *Job) error {
			fired.Add(1)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("NewScheduler: %v", err)
	}
	s.tick = 10 * time.Millisecond

	const n = 100
	jobs := make([]*Job, n)
	for i := 0; i < n; i++ {
		jobs[i] = &Job{
			Name:     fmt.Sprintf("bulk-%d", i),
			Schedule: "* * * * *",
			Command:  fmt.Sprintf("echo %d", i),
		}
		if err := s.AddJob(jobs[i]); err != nil {
			t.Fatalf("AddJob[%d]: %v", i, err)
		}
	}

	// Backdate all NextRun values so they are immediately due.
	s.mu.Lock()
	for _, j := range jobs {
		j.NextRun = time.Now().Add(-time.Second)
	}
	s.mu.Unlock()

	s.Start()
	defer s.Stop()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if fired.Load() >= n {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if fired.Load() < int64(n) {
		t.Errorf("only %d/%d jobs fired within 2s — scheduler dropped or skipped jobs", fired.Load(), n)
	}
}

// RT-CRON-6: Concurrent AddJob and RemoveJob from 20 goroutines — race detector check.
func TestRedTeam_Cron_ConcurrentAddRemove_Race(t *testing.T) {
	s, err := NewScheduler(Config{})
	if err != nil {
		t.Fatalf("NewScheduler: %v", err)
	}

	// Pre-populate 20 jobs.
	ids := make([]string, 20)
	for i := 0; i < 20; i++ {
		j := &Job{
			Name:     fmt.Sprintf("conc-job-%d", i),
			Schedule: "* * * * *",
			Command:  fmt.Sprintf("echo %d", i),
		}
		if err := s.AddJob(j); err != nil {
			t.Fatalf("AddJob[%d]: %v", i, err)
		}
		ids[i] = j.ID
	}

	var wg sync.WaitGroup
	const goroutines = 20

	// 10 goroutines remove existing jobs; 10 add new ones.
	for i := 0; i < goroutines; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			if i < 10 {
				_ = s.RemoveJob(ids[i])
			} else {
				j := &Job{
					Name:     fmt.Sprintf("new-job-%d", i),
					Schedule: "* * * * *",
					Command:  fmt.Sprintf("echo new%d", i),
				}
				_ = s.AddJob(j)
			}
		}()
	}
	wg.Wait()
	// If the race detector didn't fire, the test passes.
}

// RT-CRON-7: Double Stop must not panic (idempotency).
func TestRedTeam_Cron_DoubleStop_NoPanic(t *testing.T) {
	s, err := NewScheduler(Config{})
	if err != nil {
		t.Fatalf("NewScheduler: %v", err)
	}

	s.Start()
	s.Stop()

	// Second Stop must be a safe no-op.
	doneCh := make(chan struct{})
	go func() {
		defer close(doneCh)
		s.Stop()
	}()

	select {
	case <-doneCh:
		// Good.
	case <-time.After(time.Second):
		t.Error("second Stop() blocked — expected immediate no-op")
	}
}

// RT-CRON-8: Double Start must not launch a second goroutine (idempotency).
func TestRedTeam_Cron_DoubleStart_NoDoublefire(t *testing.T) {
	var fired atomic.Int32

	s, err := NewScheduler(Config{
		OnFire: func(job *Job) error {
			fired.Add(1)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("NewScheduler: %v", err)
	}
	s.tick = 10 * time.Millisecond

	job := &Job{
		Name:     "double-start-job",
		Schedule: "* * * * *",
		Command:  "echo hi",
	}
	if err := s.AddJob(job); err != nil {
		t.Fatalf("AddJob: %v", err)
	}
	s.mu.Lock()
	job.NextRun = time.Now().Add(-time.Second)
	s.mu.Unlock()

	// Double Start.
	s.Start()
	s.Start()
	defer s.Stop()

	time.Sleep(200 * time.Millisecond)

	// If double-start launched two goroutines, job would fire twice (or more).
	count := fired.Load()
	// After the first fire, NextRun advances by 1 minute, so subsequent ticks
	// won't re-fire. Only 1 fire is expected.
	if count > 1 {
		t.Errorf("double-Start caused %d fires; expected 1 — likely two concurrent tick loops", count)
	}
}

// RT-CRON-9: Verify the inflight deduplication gap.
//
// The Scheduler declares an `inflight` map to prevent double-firing a slow
// callback, but tickJobs never reads or writes it. When onFire takes longer
// than one tick interval, the same job fires concurrently multiple times.
//
// This test exposes the missing guard: onFire sleeps > tick duration, so
// without a working inflight check, the job fires twice before the first
// callback returns.
func TestRedTeam_Cron_InflightDedup_NotImplemented(t *testing.T) {
	var concurrent atomic.Int32 // current in-flight count
	var maxConcurrent atomic.Int32

	s, err := NewScheduler(Config{
		OnFire: func(job *Job) error {
			n := concurrent.Add(1)
			for {
				old := maxConcurrent.Load()
				if n <= old || maxConcurrent.CompareAndSwap(old, n) {
					break
				}
			}
			time.Sleep(60 * time.Millisecond) // longer than tick
			concurrent.Add(-1)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("NewScheduler: %v", err)
	}
	s.tick = 10 * time.Millisecond

	job := &Job{
		Name:     "slow-job",
		Schedule: "* * * * *",
		Command:  "echo slow",
	}
	if err := s.AddJob(job); err != nil {
		t.Fatalf("AddJob: %v", err)
	}
	s.mu.Lock()
	job.NextRun = time.Now().Add(-time.Second)
	s.mu.Unlock()

	s.Start()
	defer s.Stop()

	time.Sleep(200 * time.Millisecond) // 20 ticks; each should check inflight

	max := maxConcurrent.Load()
	if max > 1 {
		t.Errorf("inflight dedup failed: max concurrent onFire calls = %d (expected 1)", max)
	} else {
		t.Logf("inflight dedup working (max concurrent = %d)", max)
	}
}

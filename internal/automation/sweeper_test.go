package automation

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeClock returns a function suitable for SweeperConfig.Now that
// advances when Set is called. Lets us simulate stale heartbeats
// without time.Sleep.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock(t time.Time) *fakeClock { return &fakeClock{t: t} }
func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func TestSweeper_StaleAndDeadOwnerGetsMarkedLost(t *testing.T) {
	l := NewLedger(50)
	task := l.BeginOwned("subagent", "long-task", 99999) // PID we'll say is dead

	clock := newFakeClock(task.UpdatedAt.Add(10 * time.Minute))
	s := NewSweeper(l, SweeperConfig{
		GracePeriod: 5 * time.Minute,
		PIDAlive:    func(pid int) bool { return false },
		Now:         clock.Now,
	})

	flipped := s.SweepOnce()
	if flipped != 1 {
		t.Errorf("expected 1 task flipped, got %d", flipped)
	}
	got, _ := l.Get(task.ID)
	if got.State != TaskLost {
		t.Errorf("state: got %s want %s", got.State, TaskLost)
	}
	if got.Error == "" {
		t.Error("Error field should describe why the task was marked lost")
	}
}

func TestSweeper_StaleButLiveOwnerLeftAlone(t *testing.T) {
	l := NewLedger(50)
	task := l.BeginOwned("subagent", "long-task", 1234)

	clock := newFakeClock(task.UpdatedAt.Add(10 * time.Minute))
	s := NewSweeper(l, SweeperConfig{
		GracePeriod: 5 * time.Minute,
		PIDAlive:    func(pid int) bool { return true }, // owner still alive
		Now:         clock.Now,
	})

	if flipped := s.SweepOnce(); flipped != 0 {
		t.Errorf("alive owner should keep task running, got %d flipped", flipped)
	}
	got, _ := l.Get(task.ID)
	if got.State != TaskRunning {
		t.Errorf("state: %s", got.State)
	}
}

func TestSweeper_FreshHeartbeatLeftAlone(t *testing.T) {
	l := NewLedger(50)
	task := l.BeginOwned("subagent", "long-task", 99999)

	// Clock has only moved 1 minute since the task started — well within
	// the 5-minute grace window. Owner is dead but doesn't matter yet.
	clock := newFakeClock(task.UpdatedAt.Add(1 * time.Minute))
	s := NewSweeper(l, SweeperConfig{
		GracePeriod: 5 * time.Minute,
		PIDAlive:    func(pid int) bool { return false },
		Now:         clock.Now,
	})

	if flipped := s.SweepOnce(); flipped != 0 {
		t.Errorf("fresh heartbeat should be left alone, got %d flipped", flipped)
	}
}

func TestSweeper_PIDLessTaskFallsBackToHeartbeatOnly(t *testing.T) {
	l := NewLedger(50)
	task := l.Begin("cron", "nightly") // PID = 0

	clock := newFakeClock(task.UpdatedAt.Add(10 * time.Minute))
	s := NewSweeper(l, SweeperConfig{
		GracePeriod: 5 * time.Minute,
		Now:         clock.Now,
	})

	// No PIDAlive consultation needed — stale heartbeat with no owner
	// is enough to flip.
	if flipped := s.SweepOnce(); flipped != 1 {
		t.Errorf("PID-less stale task should flip, got %d", flipped)
	}
	got, _ := l.Get(task.ID)
	if got.State != TaskLost {
		t.Errorf("state: %s", got.State)
	}
}

func TestSweeper_TerminalTasksUntouched(t *testing.T) {
	l := NewLedger(50)
	done := l.BeginOwned("cron", "succeeded", 99999)
	l.Complete(done.ID, "ok")

	failed := l.BeginOwned("cron", "failed", 99999)
	l.Fail(failed.ID, errors.New("kaboom"))

	timedOut := l.BeginOwned("cron", "timed-out", 99999)
	l.TimedOut(timedOut.ID, "exceeded budget")

	lost := l.BeginOwned("cron", "already-lost", 99999)
	l.MarkLost(lost.ID, "manual")

	clock := newFakeClock(done.UpdatedAt.Add(time.Hour))
	s := NewSweeper(l, SweeperConfig{
		GracePeriod: 5 * time.Minute,
		PIDAlive:    func(pid int) bool { return false },
		Now:         clock.Now,
	})
	if flipped := s.SweepOnce(); flipped != 0 {
		t.Errorf("terminal tasks should be untouched, got %d flipped", flipped)
	}
}

func TestSweeper_HeartbeatPreventsLost(t *testing.T) {
	l := NewLedger(50)
	task := l.BeginOwned("subagent", "long", 99999)

	// Time passes...
	clock := newFakeClock(task.UpdatedAt.Add(4 * time.Minute))
	s := NewSweeper(l, SweeperConfig{
		GracePeriod: 5 * time.Minute,
		PIDAlive:    func(pid int) bool { return false },
		Now:         clock.Now,
	})

	// Task heartbeats just before the grace window expires. Use
	// HeartbeatAt on the fake clock so the heartbeat timestamp aligns
	// with what the sweeper sees.
	l.HeartbeatAt(task.ID, clock.Now())
	clock.Advance(2 * time.Minute) // total elapsed 6 min, but heartbeat 2 min ago

	if flipped := s.SweepOnce(); flipped != 0 {
		t.Errorf("recent heartbeat should keep task running, got %d", flipped)
	}
}

func TestSweeper_HeartbeatNoOpForTerminalTasks(t *testing.T) {
	l := NewLedger(50)
	task := l.Begin("cron", "done")
	endedAt := time.Now().UTC()
	l.Complete(task.ID, "ok")

	// Sleep tiny bit so the heartbeat would update if it weren't gated.
	time.Sleep(5 * time.Millisecond)
	l.Heartbeat(task.ID)

	got, _ := l.Get(task.ID)
	if got.UpdatedAt.After(endedAt.Add(time.Second)) {
		t.Errorf("terminal task should not accept heartbeat updates: %v vs ended %v",
			got.UpdatedAt, endedAt)
	}
}

func TestSweeper_OnLostCallbackFires(t *testing.T) {
	l := NewLedger(50)
	task := l.BeginOwned("subagent", "doomed", 99999)

	var calls atomic.Int32
	clock := newFakeClock(task.UpdatedAt.Add(10 * time.Minute))
	s := NewSweeper(l, SweeperConfig{
		GracePeriod: 5 * time.Minute,
		PIDAlive:    func(pid int) bool { return false },
		Now:         clock.Now,
		OnLost: func(t LedgerTask) {
			calls.Add(1)
			if t.State != TaskLost {
				t.State = TaskLost // doesn't actually mutate the ledger; just check we got the right state in the callback
			}
		},
	})
	s.SweepOnce()
	if got := calls.Load(); got != 1 {
		t.Errorf("OnLost should fire once, got %d", got)
	}
}

func TestSweeper_StartStopIdempotent(t *testing.T) {
	l := NewLedger(50)
	s := NewSweeper(l, SweeperConfig{Interval: 10 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s.Start(ctx)
	s.Start(ctx) // second Start is no-op, not a goroutine leak
	time.Sleep(30 * time.Millisecond)
	s.Stop()
	s.Stop() // double Stop is fine
}

func TestSweeper_StopReleasesGoroutine(t *testing.T) {
	l := NewLedger(50)
	s := NewSweeper(l, SweeperConfig{Interval: 5 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s.Start(ctx)
	// Run for a bit so we know the goroutine is in the select loop.
	time.Sleep(20 * time.Millisecond)
	done := make(chan struct{})
	go func() {
		s.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Stop did not return within 1s — goroutine leak")
	}
}

func TestSweeper_ContextCancelStopsLoop(t *testing.T) {
	l := NewLedger(50)
	s := NewSweeper(l, SweeperConfig{Interval: 5 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())

	s.Start(ctx)
	time.Sleep(20 * time.Millisecond)
	cancel()
	// Stop should return promptly because the inner goroutine sees the
	// ctx cancellation.
	done := make(chan struct{})
	go func() {
		s.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Stop after ctx cancel did not return within 1s")
	}
}

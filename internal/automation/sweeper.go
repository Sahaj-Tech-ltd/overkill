// Package automation — ledger sweeper. Master plan §7.1 Layer 6:
// detects runtime-missing tasks and marks them `lost` so the user
// sees real state instead of forever-running phantoms.
//
// The sweeper is what closes the loop on:
//   - daemon crashed mid-task, restart finds an orphaned running row
//   - subagent goroutine exited without calling Complete/Fail
//   - cron job process killed externally (SIGKILL, OOM)
//
// Conservative by design: a task is only flipped to `lost` when BOTH
//   - UpdatedAt is older than GracePeriod (no heartbeats), AND
//   - the owning PID (if recorded) is no longer alive
//
// PID-less tasks fall back to the heartbeat-only check. Without an
// owner PID we can't tell "stuck" from "long-running" except by time,
// so the grace period is the only signal.
package automation

import (
	"context"
	"fmt"
	"os"
	"sync"
	"syscall"
	"time"
)

// SweeperConfig configures sweep cadence + the staleness window.
type SweeperConfig struct {
	// Interval is how often the sweeper scans. Default 60s.
	Interval time.Duration
	// GracePeriod is how long a running task can go without a
	// heartbeat before becoming a candidate for `lost`. Default 5min.
	GracePeriod time.Duration
	// PIDAlive lets tests inject a fake liveness probe. Default uses
	// syscall.Kill(pid, 0) on Unix.
	PIDAlive func(pid int) bool
	// Now lets tests inject a deterministic clock.
	Now func() time.Time
	// OnLost is called once per task that gets flipped to TaskLost.
	// Receives the post-update task. Optional — used by the daemon to
	// emit a journal alert.
	OnLost func(t LedgerTask)
}

// defaultPIDAlive checks if pid is reachable via signal 0 on Unix.
// Signal 0 doesn't actually deliver anything — it just probes the
// kernel's "is this PID alive AND can this user signal it?" answer.
func defaultPIDAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// Sweeper runs the periodic reconciliation loop over a Ledger.
type Sweeper struct {
	ledger  *Ledger
	cfg     SweeperConfig
	stop    chan struct{}
	wg      sync.WaitGroup
	mu      sync.Mutex
	running bool
}

// NewSweeper wires a sweeper to a ledger with defaults applied for any
// zero-value config fields.
func NewSweeper(l *Ledger, cfg SweeperConfig) *Sweeper {
	if cfg.Interval <= 0 {
		cfg.Interval = 60 * time.Second
	}
	if cfg.GracePeriod <= 0 {
		cfg.GracePeriod = 5 * time.Minute
	}
	if cfg.PIDAlive == nil {
		cfg.PIDAlive = defaultPIDAlive
	}
	if cfg.Now == nil {
		cfg.Now = func() time.Time { return time.Now().UTC() }
	}
	return &Sweeper{ledger: l, cfg: cfg, stop: make(chan struct{})}
}

// Start launches the sweep loop in a goroutine. Idempotent — second
// call is a no-op so daemon restart paths don't fight themselves.
func (s *Sweeper) Start(ctx context.Context) {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.mu.Unlock()

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		// Fire once immediately so a daemon restart catches orphans on
		// the first sweep instead of after a full interval.
		s.SweepOnce()
		t := time.NewTicker(s.cfg.Interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-s.stop:
				return
			case <-t.C:
				s.SweepOnce()
			}
		}
	}()
}

// Stop signals the sweep loop to exit and waits for it. Idempotent.
func (s *Sweeper) Stop() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	s.running = false
	close(s.stop)
	s.mu.Unlock()
	s.wg.Wait()
	// Reset stop so a future Start works. Mutating channels under lock
	// keeps Start/Stop racing safe.
	s.mu.Lock()
	s.stop = make(chan struct{})
	s.mu.Unlock()
}

// SweepOnce performs a single reconciliation pass. Exposed so tests
// can drive deterministic sweeps without waiting on the interval
// ticker, and so the daemon can force a sweep on shutdown.
func (s *Sweeper) SweepOnce() int {
	now := s.cfg.Now()
	threshold := now.Add(-s.cfg.GracePeriod)
	flipped := 0
	for _, t := range s.ledger.List() {
		if t.State != TaskRunning && t.State != TaskQueued {
			continue
		}
		if !t.UpdatedAt.Before(threshold) {
			continue // still within grace window
		}
		// Heartbeat is stale. If we have an owner PID, require it to
		// be dead before flipping to lost — a long-running task on a
		// live process is "should heartbeat more", not "lost".
		if t.OwnerPID > 0 && s.cfg.PIDAlive(t.OwnerPID) {
			continue
		}
		reason := fmt.Sprintf("no heartbeat for %s", now.Sub(t.UpdatedAt).Round(time.Second))
		if t.OwnerPID > 0 {
			reason += fmt.Sprintf("; owner pid %d not alive", t.OwnerPID)
		}
		s.ledger.MarkLost(t.ID, reason)
		flipped++
		if s.cfg.OnLost != nil {
			if updated, ok := s.ledger.Get(t.ID); ok {
				s.cfg.OnLost(updated)
			}
		}
	}
	return flipped
}

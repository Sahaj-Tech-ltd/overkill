package automation

import (
	"fmt"
	"io"
	"log"
	"os"
	"runtime/debug"
	"sort"
	"sync"
	"time"
)

// stderrSink returns the writer for alarm-side warnings. Wrapped so
// tests can override via SetStderrSink without touching os.Stderr.
var alarmStderrSink io.Writer = os.Stderr

func stderrSink() io.Writer { return alarmStderrSink }

// SetAlarmStderrSink swaps the warning destination. Tests call this
// in TestMain or t.Cleanup to redirect noise into a captured buffer.
// Pass nil to restore os.Stderr — useful in t.Cleanup so a later test
// reading from stderr isn't stuck with the prior test's buffer.
// Concurrent-unsafe by design — tests run sequentially within a
// package and parallel writes to stderr aren't a real concern.
func SetAlarmStderrSink(w io.Writer) {
	if w == nil {
		alarmStderrSink = os.Stderr
		return
	}
	alarmStderrSink = w
}

// Alarm is a one-shot timer scheduled by the agent. On fire, the
// daemon hands Prompt to a cheap sub-agent which decides whether
// there's real work or whether to return-to-sleep without burning a
// turn on the main model.
type Alarm struct {
	ID     string    `json:"id"`
	Name   string    `json:"name"`
	FireAt time.Time `json:"fire_at"`
	// Action is the legacy shell-command field, retained so existing
	// daemon paths that shell out continue to work. New callers should
	// set Prompt instead.
	Action string `json:"action,omitempty"`
	// Prompt is the natural-language instruction the sub-agent runs
	// when the alarm fires. Example: "check if the build at /tmp/build.log
	// finished successfully; if it failed, summarise the last error."
	Prompt    string `json:"prompt,omitempty"`
	SessionID string `json:"session_id"`
	Fired     bool   `json:"fired"`
	Cancelled bool   `json:"cancelled"`
	// FiredAt records when the alarm fired so the user can audit
	// "how late was this?" if the daemon was down at FireAt.
	FiredAt time.Time `json:"fired_at,omitempty"`
	// Result is the sub-agent's one-line summary post-fire, surfaced
	// in `alarm_list` so the user can see "what happened" without
	// digging into journal entries.
	Result string `json:"result,omitempty"`
	// Attempts counts how many times we've tried to fire this alarm.
	// Each failed fire bumps it and reschedules with linear backoff;
	// after maxAlarmAttempts the alarm gives up and gets marked Fired
	// with the last error in Result. Previously a failing callback
	// left the alarm permanently stuck — no retry path at all.
	Attempts int `json:"attempts,omitempty"`
}

// AlarmClock runs the timer loop + delegates to a fire callback. The
// store is optional — when nil the clock is in-memory-only (tests,
// the no-daemon path). When non-nil, every Set/Cancel/fire mutation
// writes through to the store so a daemon restart can resume pending
// alarms.
type AlarmClock struct {
	mu      sync.RWMutex
	alarms  map[string]*Alarm
	fire    func(alarm *Alarm) error
	stop    chan struct{}
	running bool // guarded by mu; prevents double-Start / double-Stop panics
	store   AlarmStore
	now     func() time.Time // injected for tests
}

// NewAlarmClock returns a non-persistent clock. Prefer NewAlarmClockWithStore
// when wiring into the daemon.
func NewAlarmClock(fire func(alarm *Alarm) error) *AlarmClock {
	return &AlarmClock{
		alarms: make(map[string]*Alarm),
		fire:   fire,
		stop:   make(chan struct{}),
		now:    func() time.Time { return time.Now() },
	}
}

// NewAlarmClockWithStore wires persistence. The store is used for:
//   - Reload() on Start (called automatically)
//   - Save on Set/Cancel
//   - Save (overwrite) when an alarm fires
//
// Store errors are logged to stderr but never block the clock loop —
// "alarm fired but couldn't persist Fired=true" is a degraded mode,
// not a fatal one.
func NewAlarmClockWithStore(fire func(alarm *Alarm) error, store AlarmStore) *AlarmClock {
	c := NewAlarmClock(fire)
	c.store = store
	return c
}

// Start kicks off the 1s tick loop. When a store is wired, Start first
// reloads pending alarms so a daemon restart resumes seamlessly.
// Start kicks off the 1s tick loop. Idempotent — second Start is a
// real no-op (the prior version's "callers should pair" comment was
// aspirational; in practice the daemon's restart path could hit
// double-Start and panic via the closed-channel guard below). Pair
// with Stop; both are now safe to call multiple times.
func (a *AlarmClock) Start() {
	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return
	}
	a.running = true
	// Re-arm the stop channel in case this is a Start-after-Stop
	// (prior Stop closed it; we'd panic-on-close on next Stop
	// without a fresh channel).
	a.stop = make(chan struct{})
	stopCh := a.stop
	a.mu.Unlock()

	// Reload before launching the tick — if Reload finds an alarm
	// whose FireAt has already passed (daemon was down), the first
	// tick fires it within a second.
	if a.store != nil {
		if err := a.Reload(); err != nil {
			fmt.Fprintf(stderrSink(), "alarm clock: reload: %v\n", err)
		}
	}
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		defer func() {
			if r := recover(); r != nil {
				log.Printf("automation: alarm clock goroutine panic: %v\n%s", r, debug.Stack())
			}
		}()

		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
				a.checkAlarms()
			}
		}
	}()
}

// Reload pulls every persisted alarm into memory, discarding rows that
// completed more than 24h ago (keeps the store from growing unbounded
// while preserving the recent audit trail). Safe to call multiple
// times; later Reloads merge with what's already in memory.
func (a *AlarmClock) Reload() error {
	if a.store == nil {
		return nil
	}
	loaded, err := a.store.Load()
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	cutoff := a.now().Add(-24 * time.Hour)
	for _, al := range loaded {
		// Drop ancient terminal rows. We keep recent terminal rows so
		// `alarm_list` still shows "fired 30 min ago" entries.
		if (al.Fired || al.Cancelled) && al.FireAt.Before(cutoff) {
			_ = a.store.Delete(al.ID)
			continue
		}
		cp := *al
		a.alarms[al.ID] = &cp
	}
	return nil
}

func (a *AlarmClock) checkAlarms() {
	a.mu.Lock()
	// Snapshot the candidates under the lock so the fire callback can
	// take however long it needs (sub-agent runs are not 1s tasks)
	// without blocking Set/Cancel calls from other goroutines.
	//
	// We DON'T mark Fired=true here anymore. Doing so before the
	// callback ran left the alarm stuck-fired-with-error if the
	// callback failed — no path to retry. Now Fired flips only on
	// success. On failure we leave it unfired, bump FireAt by a
	// linear backoff, and let the next tick re-pick it up. A separate
	// Attempts counter caps retries so a permanently-broken callback
	// doesn't retry-storm forever.
	now := a.now()
	var due []*Alarm
	for _, alarm := range a.alarms {
		if alarm.Fired || alarm.Cancelled {
			continue
		}
		if !now.Before(alarm.FireAt) {
			cp := *alarm
			due = append(due, &cp)
		}
	}
	a.mu.Unlock()

	// Fire outside the lock so a slow fire callback doesn't stall the
	// ticker loop. Re-check Cancelled JUST before fire so a Cancel
	// that arrived after the snapshot still suppresses the callback —
	// previously a concurrent Cancel was silently overruled by the
	// in-flight fire.
	for _, alarm := range due {
		a.mu.Lock()
		live, exists := a.alarms[alarm.ID]
		if !exists || live.Cancelled || live.Fired {
			a.mu.Unlock()
			continue
		}
		a.mu.Unlock()

		err := safeFire(a.fire, alarm)

		a.mu.Lock()
		live, exists = a.alarms[alarm.ID]
		if !exists || live.Cancelled {
			// Cancelled during/after fire: don't promote to Fired.
			// Callback may have had real side effects (unavoidable —
			// we can't reach into the running callback), but the
			// alarm record reflects the user's intent.
			a.mu.Unlock()
			continue
		}
		if err != nil {
			live.Attempts++
			live.Result = "fire failed: " + err.Error()
			const maxAlarmAttempts = 3
			if live.Attempts >= maxAlarmAttempts {
				// Give up — record as fired-with-error so it's not
				// retried forever and the user can see it failed.
				live.Fired = true
				live.FiredAt = now
			} else {
				// Linear backoff: retry in 60s * attempt count.
				live.FireAt = a.now().Add(time.Duration(live.Attempts) * 60 * time.Second)
			}
		} else {
			live.Fired = true
			live.FiredAt = now
			// Capture result from the fire callback (may mutate alarm.Result).
			// If the callback didn't set a result, record a default.
			if alarm.Result != "" {
				live.Result = alarm.Result
			} else {
				live.Result = "ok"
			}
		}
		cp := *live
		a.mu.Unlock()

		if a.store != nil {
			if err := a.store.Save(&cp); err != nil {
				fmt.Fprintf(stderrSink(), "alarm clock: persist post-fire: %v\n", err)
			}
		}
	}
}

// Stop signals the tick loop to exit. Idempotent: a second Stop is a
// no-op. Old code naively `close(a.stop)`'d on every Stop which
// panicked the second time around — daemon restart paths could hit
// this.
func (a *AlarmClock) Stop() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.running {
		return
	}
	a.running = false
	close(a.stop)
}

func (a *AlarmClock) Set(alarm *Alarm) error {
	if alarm == nil || alarm.ID == "" {
		return fmt.Errorf("automation: set alarm: missing ID")
	}
	a.mu.Lock()
	// Allow overwrite of cancelled or already-fired alarms — those
	// are terminal and the user obviously wants a new alarm at this
	// ID. Only an alarm in the pending state blocks Set with
	// ErrAlreadyExists.
	if existing, exists := a.alarms[alarm.ID]; exists && !existing.Cancelled && !existing.Fired {
		a.mu.Unlock()
		return fmt.Errorf("automation: set alarm %s: %w", alarm.ID, ErrAlreadyExists)
	}
	cp := *alarm
	a.alarms[alarm.ID] = &cp
	a.mu.Unlock()

	// Persist outside the in-memory lock — the store has its own
	// concurrency story, and we don't want store latency holding
	// readers off the in-memory view.
	if a.store != nil {
		if err := a.store.Save(&cp); err != nil {
			// Persistence failed; roll back the in-memory insert so the
			// caller sees a single consistent failure path.
			a.mu.Lock()
			delete(a.alarms, alarm.ID)
			a.mu.Unlock()
			return fmt.Errorf("automation: persist alarm %s: %w", alarm.ID, err)
		}
	}
	return nil
}

func (a *AlarmClock) Cancel(id string) bool {
	a.mu.Lock()
	alarm, exists := a.alarms[id]
	if !exists {
		a.mu.Unlock()
		return false
	}
	if alarm.Cancelled {
		a.mu.Unlock()
		return true // already cancelled; idempotent
	}
	alarm.Cancelled = true
	cp := *alarm
	a.mu.Unlock()

	if a.store != nil {
		if err := a.store.Save(&cp); err != nil {
			fmt.Fprintf(stderrSink(), "alarm clock: persist cancel: %v\n", err)
		}
	}
	return true
}

func (a *AlarmClock) List() []*Alarm {
	a.mu.RLock()
	defer a.mu.RUnlock()

	result := make([]*Alarm, 0, len(a.alarms))
	for _, alarm := range a.alarms {
		cp := *alarm
		result = append(result, &cp)
	}
	// Sort by FireAt asc for deterministic iteration. Map iteration
	// order in Go is intentionally randomised; without an explicit
	// sort callers see "alarms in random order" which surprises both
	// humans (alarm_list output is unpredictable) and tests.
	sort.Slice(result, func(i, j int) bool {
		return result[i].FireAt.Before(result[j].FireAt)
	})
	return result
}

func (a *AlarmClock) Pending() []*Alarm {
	a.mu.RLock()
	defer a.mu.RUnlock()

	var pending []*Alarm
	for _, alarm := range a.alarms {
		if !alarm.Fired && !alarm.Cancelled {
			cp := *alarm
			pending = append(pending, &cp)
		}
	}

	sort.Slice(pending, func(i, j int) bool {
		return pending[i].FireAt.Before(pending[j].FireAt)
	})

	return pending
}

// safeFire wraps the user-supplied fire callback with a panic recovery.
// Without this, a panic in the callback would crash the tick goroutine
// and silence all future alarms.
func safeFire(fn func(alarm *Alarm) error, alarm *Alarm) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("alarm fire panicked: %v", r)
		}
	}()
	return fn(alarm)
}

// Package cron — activity tracking for gateway-aware cron fire dispatch.
// Cron jobs produce output asynchronously; we hold that output until the
// user is idle (no agent activity for DefaultIdleWindow), then flush
// through the gateway dispatcher so the user sees a tidy digest.
package cron

import (
	"sync"
	"time"
)

// DefaultIdleWindow is how long the agent must be idle before cron
// outputs are flushed through the gateway.
const DefaultIdleWindow = 5 * time.Minute

// IdleWindow is the active idle window used by the tracker.
// Defaults to DefaultIdleWindow; override with SetIdleWindow.
var IdleWindow = DefaultIdleWindow

// SetIdleWindow overrides the idle window at runtime.
func SetIdleWindow(d time.Duration) {
	if d > 0 {
		IdleWindow = d
	}
}

// ActivityTracker records the most recent agent activity timestamp.
// Cron outputs are held while the user is actively chatting; when the
// agent goes idle (no Record calls within DefaultIdleWindow), buffered
// cron outputs are flushed through the gateway.
type ActivityTracker struct {
	mu   sync.Mutex
	last time.Time
}

// NewActivityTracker returns a tracker seeded with the current time so
// a freshly-started daemon doesn't immediately flush on the first tick.
func NewActivityTracker() *ActivityTracker {
	return &ActivityTracker{last: time.Now()}
}

// Record marks an activity event, resetting the idle timer.
// Call this from the gateway dispatcher every time a user message
// is processed (each agent turn).
func (t *ActivityTracker) Record() {
	t.mu.Lock()
	t.last = time.Now()
	t.mu.Unlock()
}

// IdleSince returns how long since the last recorded activity.
func (t *ActivityTracker) IdleSince() time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	return time.Since(t.last)
}

// IdleFor returns true when no activity has been recorded for at least dur.
// Alias kept for compatibility with existing callers; prefer IdleFor.
func (t *ActivityTracker) IdleFor(dur time.Duration) bool {
	return t.IdleSince() >= dur
}

// IsIdle returns true when no activity has been recorded for at least dur.
func (t *ActivityTracker) IsIdle(dur time.Duration) bool {
	return t.IdleSince() >= dur
}

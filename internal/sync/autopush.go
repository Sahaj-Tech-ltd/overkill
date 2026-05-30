package sync

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
)

// pushInFlight is non-zero when an auto-push goroutine is already running.
// Prevents unbounded goroutine accumulation under slow networks.
var pushInFlight int32

// AutoPushIfEnabled fires a non-blocking push of the named session if both
// the manager and config opt in. Errors land on the supplied logFn (so the
// CLI can write them to stderr; the TUI can ignore them).
//
// Returns immediately. The caller does not need to wait — the goroutine
// uses a 30s timeout internally. Only one push goroutine is allowed
// at a time; concurrent calls are silently skipped.
func AutoPushIfEnabled(cfg *config.Config, mgr *Manager, sessionID string, logFn func(error)) {
	if cfg == nil || mgr == nil || sessionID == "" {
		return
	}
	if !cfg.Sync.AutoPush {
		return
	}
	if !atomic.CompareAndSwapInt32(&pushInFlight, 0, 1) {
		return
	}
	go func() {
		defer atomic.StoreInt32(&pushInFlight, 0)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := mgr.PushOne(ctx, sessionID); err != nil && logFn != nil {
			logFn(err)
		}
	}()
}

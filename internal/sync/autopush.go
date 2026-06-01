package sync

import (
	"context"
	"log"
	"runtime/debug"
	"sync/atomic"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
)

// pushTimeout holds the autopush timeout in nanoseconds.
// Use getPushTimeout() / SetPushTimeout() to access safely.
var pushTimeout atomic.Int64

func init() { pushTimeout.Store(int64(30 * time.Second)) }

// SetPushTimeout overrides the autopush timeout from config.
func SetPushTimeout(sec int) {
	if sec > 0 {
		pushTimeout.Store(int64(time.Duration(sec) * time.Second))
	}
}

// getPushTimeout returns the current autopush timeout.
func getPushTimeout() time.Duration {
	return time.Duration(pushTimeout.Load())
}

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
		defer func() {
			if r := recover(); r != nil {
				log.Printf("sync: autopush goroutine panic: %v\n%s", r, debug.Stack())
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), getPushTimeout())
		defer cancel()
		if err := mgr.PushOne(ctx, sessionID); err != nil {
			if logFn != nil {
				logFn(err)
			} else {
				log.Printf("[autopush] %v", err)
			}
		}
	}()
}

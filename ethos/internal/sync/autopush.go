package sync

import (
	"context"
	"time"

	"github.com/Sahaj-Tech-ltd/ethos/internal/config"
)

// AutoPushIfEnabled fires a non-blocking push of the named session if both
// the manager and config opt in. Errors land on the supplied logFn (so the
// CLI can write them to stderr; the TUI can ignore them).
//
// Returns immediately. The caller does not need to wait — the goroutine
// uses a 30s timeout internally.
func AutoPushIfEnabled(cfg *config.Config, mgr *Manager, sessionID string, logFn func(error)) {
	if cfg == nil || mgr == nil || sessionID == "" {
		return
	}
	if !cfg.Sync.AutoPush {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := mgr.PushOne(ctx, sessionID); err != nil && logFn != nil {
			logFn(err)
		}
	}()
}

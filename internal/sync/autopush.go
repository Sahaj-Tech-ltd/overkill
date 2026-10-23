package sync

import (
	"context"
	"errors"
	"log"
	"runtime/debug"
	"sync/atomic"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
)

// pushTimeout holds the per-upload timeout, independent of command shutdown.
var pushTimeout atomic.Int64

func init() { pushTimeout.Store(int64(30 * time.Second)) }
func SetPushTimeout(sec int) {
	if sec > 0 {
		pushTimeout.Store(int64(time.Duration(sec) * time.Second))
	}
}
func getPushTimeout() time.Duration { return time.Duration(pushTimeout.Load()) }

type autoPush struct {
	logFn   func(error)
	timeout time.Duration
}

// AutoPushIfEnabled queues the latest persisted state of a session. One worker
// belongs to each manager; a slow upload cannot silently discard the next turn
// or prevent a different application's manager from uploading.
func AutoPushIfEnabled(cfg *config.Config, mgr *Manager, sessionID string, logFn func(error)) {
	if cfg == nil || mgr == nil || sessionID == "" || !cfg.Sync.AutoPush {
		return
	}
	mgr.autoMu.Lock()
	if mgr.autoClosed {
		mgr.autoMu.Unlock()
		reportPushError(logFn, errors.New("sync: auto-push manager closed"))
		return
	}
	if mgr.autoPending == nil {
		mgr.autoPending = make(map[string]autoPush)
	}
	timeout := getPushTimeout()
	if cfg.Sync.PushTimeoutSec > 0 {
		timeout = time.Duration(cfg.Sync.PushTimeoutSec) * time.Second
	}
	mgr.autoPending[sessionID] = autoPush{logFn: logFn, timeout: timeout}
	if mgr.autoDone == nil {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		mgr.autoDone, mgr.autoCancel = done, cancel
		go mgr.runAutoPush(ctx, cancel, done)
	}
	mgr.autoMu.Unlock()
}

func reportPushError(logFn func(error), err error) {
	if logFn != nil {
		logFn(err)
	} else {
		log.Printf("[autopush] %v", err)
	}
}

func (m *Manager) runAutoPush(ctx context.Context, cancel context.CancelFunc, done chan struct{}) {
	defer cancel()
	for {
		m.autoMu.Lock()
		if len(m.autoPending) == 0 || ctx.Err() != nil {
			m.autoPending = nil
			m.autoDone, m.autoCancel = nil, nil
			close(done)
			m.autoMu.Unlock()
			return
		}
		var id string
		var job autoPush
		for id, job = range m.autoPending {
			break
		}
		delete(m.autoPending, id)
		m.autoMu.Unlock()
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("sync: autopush goroutine panic: %v\n%s", r, debug.Stack())
				}
			}()
			uploadCtx, stop := context.WithTimeout(ctx, job.timeout)
			defer stop()
			if err := m.PushOne(uploadCtx, id); err != nil {
				reportPushError(job.logFn, err)
			}
		}()
	}
}

// CloseAutoPush stops admission and drains already queued uploads. On expiry it
// cancels the worker and joins it before the owner closes its session store.
// Backends must honor context cancellation, as required by Backend's contract.
func (m *Manager) CloseAutoPush(ctx context.Context) error {
	if m == nil {
		return nil
	}
	m.autoMu.Lock()
	m.autoClosed = true
	done, cancel := m.autoDone, m.autoCancel
	m.autoMu.Unlock()
	if done == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		cancel()
		<-done
		return ctx.Err()
	}
}

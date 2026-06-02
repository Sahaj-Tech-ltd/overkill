// Package main — daemon-side estop RPC. When the CLI runs
// `overkill estop`, it first tries this graceful RPC path: the daemon
// cancels every running task and replies with the count of broadcasts
// sent. If the RPC fails or the daemon isn't running, the CLI falls
// back to the signal-based kill cascade (estop.go).
//
// Graceful first because:
//   - signals are coarse — SIGTERM the daemon and you lose its
//     ability to log the halt cleanly
//   - in-flight tool calls running under daemon-owned contexts get a
//     chance to flush state before exit
//   - tool receipts (the audit chain) are preserved instead of being
//     truncated mid-write
package main

import (
	"context"
	"encoding/json"

	"github.com/Sahaj-Tech-ltd/overkill/internal/daemon"
)

// estopBroadcaster is the contract the daemon implements: "halt
// everything you own and tell me how many handles you signalled". The
// returned count is informational (TUI/CLI shows "halted N tasks"); a
// zero count is success, not an error.
type estopBroadcaster interface {
	BroadcastEStop() int
}

// estopHandler returns an RPC handler that broadcasts to the supplied
// estopBroadcaster. The broadcaster typically owns the alarm clock,
// SOP engine, and any running task contexts.
func estopHandler(b estopBroadcaster) daemon.Handler {
	return func(ctx context.Context, req daemon.Request) (daemon.Response, error) {
		count := 0
		if b != nil {
			count = b.BroadcastEStop()
		}
		out, _ := json.Marshal(map[string]int{"halted": count})
		return daemon.Response{Result: out}, nil
	}
}

// daemonEStopBroadcaster gathers every running subsystem and cancels
// each in turn. Today's surface: alarms (pending → cancelled). Future
// extensions: SOP engine, in-flight stream contexts.
type daemonEStopBroadcaster struct {
	alarmCancelAll func() int
}

func (b *daemonEStopBroadcaster) BroadcastEStop() int {
	if b == nil {
		return 0
	}
	count := 0
	if b.alarmCancelAll != nil {
		count += b.alarmCancelAll()
	}
	return count
}

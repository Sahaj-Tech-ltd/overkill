package main

import (
	"sync/atomic"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/daemon"
)

// routineEventForwarder ships agent lifecycle events to the daemon's
// routine engine via the RPC socket. Best-effort and non-blocking:
//
//   - One-shot connection per fire, capped to 250ms — we never let
//     a slow daemon throttle the agent loop.
//   - If the daemon is down (ErrDaemonDown) we mark the forwarder
//     as offline for 30s and stop trying. After the cooldown, the
//     next fire attempts a reconnect.
//   - Routine fire errors are swallowed; this is fire-and-forget
//     telemetry shape, not a request/response pattern.
//
// This split keeps the agent process clean of daemon-state knowledge
// and lets the daemon (or its absence) be the source of truth for
// "are routines active?".
type routineEventForwarder struct {
	// offlineUntil is a unix-nano timestamp; while now < offlineUntil
	// we skip the RPC. Atomic so concurrent fires from the agent's
	// emit() goroutines coordinate without a mutex.
	offlineUntil atomic.Int64
}

func newRoutineEventForwarder() *routineEventForwarder {
	return &routineEventForwarder{}
}

const routineForwardBackoff = 30 * time.Second

// Fire delivers one event to the daemon's routine engine. Returns
// nothing — telemetry shape only.
func (f *routineEventForwarder) Fire(event string) {
	if event == "" {
		return
	}
	if time.Now().UnixNano() < f.offlineUntil.Load() {
		return
	}
	path, err := daemon.SocketPath()
	if err != nil {
		return
	}
	client := daemon.NewClient(path).WithTimeout(250 * time.Millisecond)
	_, err = client.Call("routine.fire", map[string]any{"trigger": event})
	if err != nil {
		// Mark offline only on hard "daemon down" errors. A handler
		// error (e.g. routine misconfigured) should NOT take the
		// forwarder offline — those are per-routine issues.
		if err == daemon.ErrDaemonDown {
			f.offlineUntil.Store(time.Now().Add(routineForwardBackoff).UnixNano())
		}
	}
}

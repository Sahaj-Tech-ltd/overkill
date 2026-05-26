// Package main — alarm RPC. Exposes the daemon's in-process alarm
// clock to TUI / CLI clients over the daemon socket. Three ops:
// alarm.set, alarm.list, alarm.cancel.
//
// The daemon owns the Badger-backed AlarmClock; clients can't open
// the same Badger dir simultaneously (single-process constraint).
// Routing writes + reads through the socket keeps the constraint
// honest and lets the daemon be the single timing authority.
package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Sahaj-Tech-ltd/overkill/internal/automation"
	"github.com/Sahaj-Tech-ltd/overkill/internal/daemon"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools"
)

// registerAlarmHandlers wires alarm ops onto a running daemon socket
// server. Called from runDaemonStart after the AlarmClock is up.
func registerAlarmHandlers(srv *daemon.Server, clock *automation.AlarmClock) {
	srv.Register("alarm.set", alarmSetHandler(clock))
	srv.Register("alarm.list", alarmListHandler(clock))
	srv.Register("alarm.cancel", alarmCancelHandler(clock))
}

func alarmSetHandler(clock *automation.AlarmClock) daemon.Handler {
	return func(ctx context.Context, req daemon.Request) (daemon.Response, error) {
		var a automation.Alarm
		if err := json.Unmarshal(req.Params, &a); err != nil {
			return daemon.Response{}, fmt.Errorf("alarm.set: parse: %w", err)
		}
		if err := clock.Set(&a); err != nil {
			return daemon.Response{}, err
		}
		b, _ := json.Marshal(a)
		return daemon.Response{Result: b}, nil
	}
}

func alarmListHandler(clock *automation.AlarmClock) daemon.Handler {
	return func(ctx context.Context, req daemon.Request) (daemon.Response, error) {
		all := clock.List()
		b, err := json.Marshal(all)
		if err != nil {
			return daemon.Response{}, err
		}
		return daemon.Response{Result: b}, nil
	}
}

type alarmCancelParams struct {
	ID string `json:"id"`
}

func alarmCancelHandler(clock *automation.AlarmClock) daemon.Handler {
	return func(ctx context.Context, req daemon.Request) (daemon.Response, error) {
		var p alarmCancelParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return daemon.Response{}, fmt.Errorf("alarm.cancel: parse: %w", err)
		}
		ok := clock.Cancel(p.ID)
		b, _ := json.Marshal(map[string]bool{"cancelled": ok})
		return daemon.Response{Result: b}, nil
	}
}

// daemonAlarmGateway is the AlarmGateway implementation that talks to
// a running daemon over its UNIX socket. Used by the TUI so the same
// alarm_set/list/cancel tools work without giving the TUI direct
// access to Badger.
//
// Failure modes:
//   - daemon down → ErrDaemonDown bubbles up, alarm_set surfaces a
//     "is the daemon running?" message to the user.
//   - socket present but stuck → 5s default client timeout in
//     daemon.Client trips, returns error.
type daemonAlarmGateway struct {
	client *daemon.Client
}

// newDaemonAlarmGateway builds a gateway against the standard daemon
// socket path. Cheap — the underlying client opens fresh connections
// per call, so construction has no side effects.
func newDaemonAlarmGateway() (tools.AlarmGateway, error) {
	path, err := daemon.SocketPath()
	if err != nil {
		return nil, fmt.Errorf("alarm gateway: socket path: %w", err)
	}
	return &daemonAlarmGateway{client: daemon.NewClient(path)}, nil
}

func (g *daemonAlarmGateway) Set(a *automation.Alarm) error {
	if a == nil {
		return fmt.Errorf("alarm gateway: nil alarm")
	}
	_, err := g.client.Call("alarm.set", a)
	return err
}

func (g *daemonAlarmGateway) Cancel(id string) bool {
	raw, err := g.client.Call("alarm.cancel", alarmCancelParams{ID: id})
	if err != nil {
		return false
	}
	var out struct {
		Cancelled bool `json:"cancelled"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return false
	}
	return out.Cancelled
}

func (g *daemonAlarmGateway) List() []*automation.Alarm {
	raw, err := g.client.Call("alarm.list", nil)
	if err != nil {
		return nil
	}
	var out []*automation.Alarm
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}

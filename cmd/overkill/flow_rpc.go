// Package main — flow RPC. The TUI's agent shouldn't open the
// daemon's Badger DB directly (single-process constraint). When the
// TUI's stream loop hits maxSteps it serialises the FlowState and
// asks the daemon to persist it via "flow.checkpoint". The daemon's
// alarm dispatcher reads from the same DB on resume.
//
// Resume reads (List, Load) aren't exposed today — the daemon does
// its own resume via the alarm fire callback. If a future feature
// needs to inspect flow records from the TUI (e.g. an "active flows"
// sidebar), add `flow.list` here.
package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/automation"
	"github.com/Sahaj-Tech-ltd/overkill/internal/daemon"
)

// registerFlowHandlers wires flow ops onto a running daemon socket.
// The store may be nil (daemon started without automation Badger) —
// in that case the handlers reject with a clear error rather than
// panicking on the nil receiver.
func registerFlowHandlers(srv *daemon.Server, store agent.FlowStore, alarmClock *automation.AlarmClock) {
	srv.Register("flow.checkpoint", flowCheckpointHandler(store, alarmClock))
}

// flowCheckpointParams is what the TUI sends. Mirror of FlowState but
// labeled separately so we can extend params without breaking the
// on-disk shape.
type flowCheckpointParams struct {
	State *agent.FlowState `json:"state"`
	// ResumeAfter is how long to wait before firing a resume alarm.
	// "5m" by default; the TUI can override for testing.
	ResumeAfter string `json:"resume_after,omitempty"`
}

// daemonFlowSink is the TUI-side FlowStore.Save adapter. Save calls
// the daemon's flow.checkpoint over the socket; the daemon persists
// and schedules a resume alarm. Load/Delete/List are NOT supported
// from the TUI side — the TUI's agent only writes checkpoints, the
// daemon's alarm dispatcher does the reading.
type daemonFlowSink struct {
	client *daemon.Client
}

// newDaemonFlowSink returns a TUI-side sink wired to the standard
// daemon socket. Returns nil + err when the socket path can't be
// resolved (HOME unreadable).
func newDaemonFlowSink() (*daemonFlowSink, error) {
	path, err := daemon.SocketPath()
	if err != nil {
		return nil, fmt.Errorf("flow sink: socket path: %w", err)
	}
	return &daemonFlowSink{client: daemon.NewClient(path)}, nil
}

// Save serialises the state and posts to flow.checkpoint. The daemon
// schedules the resume alarm with a 5-minute delay by default.
func (s *daemonFlowSink) Save(state *agent.FlowState) error {
	if state == nil {
		return fmt.Errorf("flow sink: nil state")
	}
	_, err := s.client.Call("flow.checkpoint", flowCheckpointParams{State: state})
	return err
}

// Load returns nil — the TUI doesn't read flow state. Implementing
// the method to satisfy agent.FlowStore; resume happens daemon-side.
func (s *daemonFlowSink) Load(id string) (*agent.FlowState, error) {
	return nil, fmt.Errorf("flow sink: Load is daemon-only")
}

// Delete is also daemon-only; the daemon's resume path cleans up.
func (s *daemonFlowSink) Delete(id string) error {
	return fmt.Errorf("flow sink: Delete is daemon-only")
}

// List is daemon-only.
func (s *daemonFlowSink) List() ([]*agent.FlowState, error) {
	return nil, fmt.Errorf("flow sink: List is daemon-only")
}

func flowCheckpointHandler(store agent.FlowStore, alarmClock *automation.AlarmClock) daemon.Handler {
	return func(ctx context.Context, req daemon.Request) (daemon.Response, error) {
		if store == nil {
			return daemon.Response{}, fmt.Errorf("flow.checkpoint: store not wired")
		}
		var p flowCheckpointParams
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return daemon.Response{}, fmt.Errorf("flow.checkpoint: parse: %w", err)
		}
		if p.State == nil || p.State.ID == "" {
			return daemon.Response{}, fmt.Errorf("flow.checkpoint: missing state.id")
		}
		if err := store.Save(p.State); err != nil {
			return daemon.Response{}, err
		}
		// Schedule a resume alarm if the alarm clock is wired. We use
		// the agent.FormatResumePrompt helper so the prefix is in one
		// place — the alarm dispatcher pattern-matches on it.
		if alarmClock != nil {
			scheduleResumeAlarm(alarmClock, p.State.ID, p.ResumeAfter)
		}
		b, _ := json.Marshal(p.State)
		return daemon.Response{Result: b}, nil
	}
}

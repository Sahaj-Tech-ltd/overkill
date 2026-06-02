package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Sahaj-Tech-ltd/overkill/internal/automation"
	"github.com/Sahaj-Tech-ltd/overkill/internal/daemon"
)

// registerRoutineHandlers exposes the routine engine over the daemon
// RPC socket so the agent process (which is separate from the
// daemon) can deliver lifecycle events without sharing in-process
// state. Three handlers:
//
//   - routine.fire(trigger): the agent ships an event by name. The
//     daemon's engine finds matching routines, respects their
//     cooldowns, and runs the action. Returns the count fired.
//   - routine.list: snapshot for the TUI/CLI sidebar.
//   - routine.toggle: enable/disable from another process.
func registerRoutineHandlers(srv *daemon.Server, engine *automation.RoutineEngine) {
	srv.Register("routine.fire", routineFireHandler(engine))
	srv.Register("routine.list", routineListHandler(engine))
	srv.Register("routine.toggle", routineToggleHandler(engine))
}

func routineFireHandler(engine *automation.RoutineEngine) daemon.Handler {
	return func(_ context.Context, req daemon.Request) (daemon.Response, error) {
		var p struct {
			Trigger string `json:"trigger"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return daemon.Response{}, fmt.Errorf("routine.fire: parse: %w", err)
		}
		if p.Trigger == "" {
			return daemon.Response{}, fmt.Errorf("routine.fire: trigger required")
		}
		fired, err := engine.HandleEvent(p.Trigger)
		if err != nil {
			return daemon.Response{}, err
		}
		body, _ := json.Marshal(map[string]any{"fired": fired, "trigger": p.Trigger})
		return daemon.Response{Result: body}, nil
	}
}

func routineListHandler(engine *automation.RoutineEngine) daemon.Handler {
	return func(_ context.Context, _ daemon.Request) (daemon.Response, error) {
		body, err := json.Marshal(engine.List())
		if err != nil {
			return daemon.Response{}, err
		}
		return daemon.Response{Result: body}, nil
	}
}

func routineToggleHandler(engine *automation.RoutineEngine) daemon.Handler {
	return func(_ context.Context, req daemon.Request) (daemon.Response, error) {
		var p struct {
			ID      string `json:"id"`
			Enabled bool   `json:"enabled"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return daemon.Response{}, fmt.Errorf("routine.toggle: parse: %w", err)
		}
		if p.ID == "" {
			return daemon.Response{}, fmt.Errorf("routine.toggle: id required")
		}
		var err error
		if p.Enabled {
			err = engine.Enable(p.ID)
		} else {
			err = engine.Disable(p.ID)
		}
		if err != nil {
			return daemon.Response{}, err
		}
		body, _ := json.Marshal(map[string]any{"id": p.ID, "enabled": p.Enabled})
		return daemon.Response{Result: body}, nil
	}
}

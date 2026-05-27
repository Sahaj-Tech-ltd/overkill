// Package tools — alarm_set / alarm_list / alarm_cancel tools exposed
// to the agent. These wrap an AlarmGateway so the same tool surface
// works against either:
//
//   - The daemon's in-process AlarmClock (when registered inside the
//     daemon binary itself — used by SOP steps, future autonomous
//     scheduling, etc.)
//   - A daemon RPC client (when registered inside the TUI — alarm_set
//     dials the daemon socket, daemon owns the Badger DB)
//
// Badger is single-process, so the TUI cannot open the alarm store
// directly while the daemon is running. The split through AlarmGateway
// keeps that constraint honest without duplicating the tool logic.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Sahaj-Tech-ltd/overkill/internal/automation"
)

// AlarmGateway is the small surface the alarm tools need. Both
// automation.AlarmClock (in-process daemon use) and a daemon RPC
// client (TUI use) satisfy it structurally — Set/Cancel/List
// signatures match. No wrapper required, just pass either.
type AlarmGateway interface {
	Set(*automation.Alarm) error
	Cancel(id string) bool
	List() []*automation.Alarm
}

// alarmSetTool implements `alarm_set`. The agent passes a fire-time
// (either an absolute RFC3339 timestamp OR a relative "in 5m"/"in 1h"
// duration string) plus a natural-language prompt describing what the
// sub-agent should do when the alarm fires.
type alarmSetTool struct {
	gw        AlarmGateway
	sessionID func() string
}

type alarmSetInput struct {
	Name   string `json:"name"`
	When   string `json:"when"`   // RFC3339 OR "in <duration>" OR "+<duration>"
	Prompt string `json:"prompt"` // natural-language instruction
}

type alarmSetOutput struct {
	ID     string    `json:"id"`
	FireAt time.Time `json:"fire_at"`
}

// NewAlarmSetTool returns the alarm_set tool. sessionID is called at
// Execute time so the tool always stamps the CURRENT session id, even
// if the agent migrated sessions between registration and call.
func NewAlarmSetTool(gw AlarmGateway, sessionID func() string) Tool {
	if sessionID == nil {
		sessionID = func() string { return "" }
	}
	return &alarmSetTool{gw: gw, sessionID: sessionID}
}

func (t *alarmSetTool) Name() string { return "alarm_set" }

func (t *alarmSetTool) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	var in alarmSetInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("alarm_set: parse: %w", err)
	}
	in.Name = strings.TrimSpace(in.Name)
	in.When = strings.TrimSpace(in.When)
	in.Prompt = strings.TrimSpace(in.Prompt)
	if in.When == "" {
		return nil, fmt.Errorf("alarm_set: 'when' is required")
	}
	if in.Prompt == "" {
		return nil, fmt.Errorf("alarm_set: 'prompt' is required — say what should happen when the alarm fires")
	}
	when, err := parseAlarmWhen(in.When)
	if err != nil {
		return nil, fmt.Errorf("alarm_set: %w", err)
	}
	if t.gw == nil {
		return nil, fmt.Errorf("alarm_set: alarm gateway not wired — is the daemon running?")
	}

	alarm := &automation.Alarm{
		ID:        uuid.NewString(),
		Name:      in.Name,
		FireAt:    when,
		Prompt:    in.Prompt,
		SessionID: t.sessionID(),
	}
	if alarm.Name == "" {
		alarm.Name = firstWords(in.Prompt, 40)
	}
	if err := t.gw.Set(alarm); err != nil {
		return nil, fmt.Errorf("alarm_set: %w", err)
	}
	out, _ := json.Marshal(alarmSetOutput{ID: alarm.ID, FireAt: alarm.FireAt})
	return out, nil
}

// alarmListTool surfaces pending and recent alarms so the agent can
// recall what it has scheduled. Returns up to 20 entries — the user
// can drill into the journal for older ones.
type alarmListTool struct {
	gw AlarmGateway
}

func NewAlarmListTool(gw AlarmGateway) Tool { return &alarmListTool{gw: gw} }

func (t *alarmListTool) Name() string { return "alarm_list" }

type alarmListEntry struct {
	ID     string    `json:"id"`
	Name   string    `json:"name"`
	FireAt time.Time `json:"fire_at"`
	Prompt string    `json:"prompt,omitempty"`
	State  string    `json:"state"`
	Result string    `json:"result,omitempty"`
}

func (t *alarmListTool) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	if t.gw == nil {
		return nil, fmt.Errorf("alarm_list: alarm gateway not wired")
	}
	all := t.gw.List()
	out := make([]alarmListEntry, 0, len(all))
	for _, a := range all {
		out = append(out, alarmListEntry{
			ID:     a.ID,
			Name:   a.Name,
			FireAt: a.FireAt,
			Prompt: a.Prompt,
			State:  alarmState(a),
			Result: a.Result,
		})
	}
	// Cap at 20 newest-first so a long-running daemon doesn't dump
	// every alarm ever into the agent's context.
	if len(out) > 20 {
		out = out[:20]
	}
	b, _ := json.Marshal(out)
	return b, nil
}

// alarmCancelTool drops an alarm before it fires.
type alarmCancelTool struct {
	gw AlarmGateway
}

func NewAlarmCancelTool(gw AlarmGateway) Tool { return &alarmCancelTool{gw: gw} }

func (t *alarmCancelTool) Name() string { return "alarm_cancel" }

type alarmCancelInput struct {
	ID string `json:"id"`
}

func (t *alarmCancelTool) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	var in alarmCancelInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("alarm_cancel: parse: %w", err)
	}
	in.ID = strings.TrimSpace(in.ID)
	if in.ID == "" {
		return nil, fmt.Errorf("alarm_cancel: 'id' is required")
	}
	if t.gw == nil {
		return nil, fmt.Errorf("alarm_cancel: alarm gateway not wired")
	}
	if !t.gw.Cancel(in.ID) {
		return nil, fmt.Errorf("alarm_cancel: unknown alarm %s", in.ID)
	}
	return json.RawMessage(`{"cancelled":true}`), nil
}

// parseAlarmWhen accepts RFC3339, "in <duration>", "+<duration>", and
// bare durations ("5m"). Relative forms resolve against time.Now() so
// the tool surface is friendly: an LLM can say "in 10 minutes" without
// computing an absolute timestamp.
func parseAlarmWhen(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	// Strip the common "in " / "+" prefixes used by humans.
	for _, prefix := range []string{"in ", "+", "in"} {
		if strings.HasPrefix(s, prefix) {
			s = strings.TrimSpace(strings.TrimPrefix(s, prefix))
			break
		}
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid time format %q: want RFC3339 (2026-05-13T18:00:00Z) or duration (5m, 1h30m)", s)
	}
	if d < 0 {
		return time.Time{}, fmt.Errorf("alarm time must be in the future, got %s", d)
	}
	return time.Now().Add(d), nil
}

// alarmState collapses Fired+Cancelled flags into a single string for
// the agent. Order matters: cancelled wins over fired since a user-
// cancelled alarm is more interesting to surface than a stale fired
// flag.
func alarmState(a *automation.Alarm) string {
	switch {
	case a.Cancelled:
		return "cancelled"
	case a.Fired:
		return "fired"
	default:
		return "pending"
	}
}

// firstWords returns up to maxChars of s, trimmed at the nearest word
// boundary. Used when the agent set an alarm without an explicit Name —
// we lift the first few words of the prompt as a label.
func firstWords(s string, maxChars int) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxChars {
		return s
	}
	cut := s[:maxChars]
	if idx := strings.LastIndex(cut, " "); idx > 10 {
		cut = cut[:idx]
	}
	return cut + "…"
}

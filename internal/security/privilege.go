// Package security — privilege separation (master plan §4.3 / paper #21).
//
// PrivilegeMode toggles whether write-like tool calls are permitted. The
// reader/planner phase runs in ReaderMode; once the user confirms the plan
// the agent flips to WriterMode. This keeps an over-eager planner from
// silently mutating the repo.
//
// Enforcement lives at the tool layer: the agent's pre-tool hook asks
// PrivilegeGate.Allow(toolName, input) before dispatching.
package security

import (
	"encoding/json"
	"errors"
	"sync"
)

// PrivilegeMode is "reader" (read-only) or "writer" (full).
type PrivilegeMode string

const (
	ModeReader PrivilegeMode = "reader"
	ModeWriter PrivilegeMode = "writer"
)

// ErrWriteDenied is returned when a write-like call is blocked by ReaderMode.
var ErrWriteDenied = errors.New("privilege: write denied — agent is in reader mode")

// PrivilegeGate gates tool calls based on the current mode. Thread-safe.
type PrivilegeGate struct {
	mu   sync.RWMutex
	mode PrivilegeMode
}

// NewPrivilegeGate starts in writer mode (preserves legacy behavior). Callers
// flip to reader for plan-only sessions.
func NewPrivilegeGate(start PrivilegeMode) *PrivilegeGate {
	if start == "" {
		start = ModeWriter
	}
	return &PrivilegeGate{mode: start}
}

// Mode returns the current mode.
func (g *PrivilegeGate) Mode() PrivilegeMode {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.mode
}

// SetMode switches the mode. Returns the previous mode.
func (g *PrivilegeGate) SetMode(m PrivilegeMode) PrivilegeMode {
	if m != ModeReader && m != ModeWriter {
		return g.Mode()
	}
	g.mu.Lock()
	prev := g.mode
	g.mode = m
	g.mu.Unlock()
	return prev
}

// Allow reports whether a tool call is permitted under the current mode.
// Always true in writer mode. Reader mode permits only classified observations;
// opaque commands/adapters and malformed or mutating actions require writer mode.
func (g *PrivilegeGate) Allow(toolName string, rawInput json.RawMessage) (bool, string) {
	if g.Mode() == ModeWriter {
		return true, ""
	}
	if !IsWriteLikeTool(toolName, rawInput) {
		return true, ""
	}
	return false, "tool " + toolName + " requires writer authority and is blocked in reader mode"
}

// Reader mode admits explicitly classified observations. Arbitrary command
// execution and unknown adapters require writer authority: command text cannot
// establish absence of redirections, interpreters, hooks or remote side effects.
// This is tool admission, not an OS sandbox for trusted hooks/plugins.
func IsWriteLikeTool(name string, raw json.RawMessage) bool {
	switch name {
	case "grep", "lsp_hover", "lsp_definition", "lsp_references", "lsp_symbols",
		"ask_user", "acp_status", "acp_await", "acp_sessions", "agent_inbox",
		"agent_task_status", "agent_task_graph", "agent_await", "get_goal",
		"plan_status", "task_list", "checkpoint_list", "tag_list",
		"journal_search", "journal_timeline", "journal_get", "memory_recall", "memory_search", "memory_describe", "memory_expand":
		return false
	case "canvas":
		var in struct {
			Action string `json:"action"`
		}
		return json.Unmarshal(raw, &in) != nil || in.Action != "state"
	case "fs":
		var in struct {
			Action string `json:"action"`
		}
		if json.Unmarshal(raw, &in) != nil {
			return true
		}
		switch in.Action {
		case "read", "glob", "stat":
			return false
		}
		return true
	case "agent_contract":
		var in struct {
			Action string `json:"action"`
		}
		if json.Unmarshal(raw, &in) != nil {
			return true
		}
		switch in.Action {
		case "get", "list", "readiness":
			return false
		}
		return true
	default:
		return true
	}
}

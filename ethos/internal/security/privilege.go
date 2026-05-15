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
	"regexp"
	"strings"
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
// Always true in writer mode. In reader mode, returns false for tools known
// to mutate state (filesystem, git push, network POST without exemption).
func (g *PrivilegeGate) Allow(toolName string, rawInput json.RawMessage) (bool, string) {
	if g.Mode() == ModeWriter {
		return true, ""
	}
	if !IsWriteLikeTool(toolName, rawInput) {
		return true, ""
	}
	return false, "tool " + toolName + " mutates state and is blocked in reader mode"
}

// writeLikeTools enumerates tools that always mutate state. The list mirrors
// the subagent enforcer; kept independent so neither package depends on the
// other.
var writeLikeTools = map[string]bool{
	"fs_write":         true,
	"patch":            true,
	"worktree_add":     true,
	"worktree_remove":  true,
	"memory_remember":  true,
	"memory_forget":    true,
	"checkpoint_snapshot": true, // technically read-of-files-then-copy, but mutates ~/.overkill
	"checkpoint_restore":  true,
	"regression_record":   true,
	"acp_send":            true,
	"browser_dev":         true, // can mutate live browser session
}

// shellWriteRe catches a leading shell verb that suggests a write.
// Extended to cover destructive verbs the original missed (ln/dd/mkfs/
// mount/umount/iptables/nft/systemctl/service/kill/pkill/killall/
// truncate/unlink, plus shutdown family). Anything that mutates the
// filesystem, devices, network state, or process/system state belongs
// here so the privilege gate can decide whether a confirmation is
// needed.
var shellWriteRe = regexp.MustCompile(`^\s*(rm|mv|cp|ln|dd|mkdir|touch|tee|chmod|chown|chgrp|truncate|unlink|sed\s+-i|>{1,2}\s*\S+|cat\s*>{1,2}|mkfs(\.\S+)?|mount|umount|iptables|nft|systemctl|service|kill|pkill|killall|shutdown|reboot|poweroff|halt|git\s+(push|commit|merge|reset|rebase|checkout|clean))`)

// IsWriteLikeTool classifies a single call. Exported so other layers can
// share the same heuristic (subagent enforcer, ledger annotations, etc.).
func IsWriteLikeTool(name string, raw json.RawMessage) bool {
	if writeLikeTools[name] {
		return true
	}
	switch name {
	case "shell", "pty_shell":
		var sh struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal(raw, &sh); err != nil {
			return false
		}
		return shellWriteRe.MatchString(sh.Command)
	case "fs":
		var f struct {
			Action string `json:"action"`
		}
		if err := json.Unmarshal(raw, &f); err != nil {
			return false
		}
		switch strings.ToLower(f.Action) {
		case "write", "create", "delete", "remove", "mkdir":
			return true
		}
	case "git":
		var g struct {
			Subcommand string `json:"subcommand"`
			Args       string `json:"args"`
		}
		if err := json.Unmarshal(raw, &g); err != nil {
			return false
		}
		sub := strings.ToLower(g.Subcommand)
		switch sub {
		case "push", "commit", "merge", "reset", "rebase", "checkout":
			return true
		}
	}
	return false
}

package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// preToolScan runs the installed security scanners against the COMMAND
// inside a risky tool call (shell, pty_shell, etc.) before execution.
// Without this, a jailbroken model can synthesise `rm -rf /` in writer
// mode and the scanners never see it — they only ran on the original
// user message (master plan §4.3 "Pre-Exec Command Scanner").
//
// Returns (blocked=false, "") when nothing to do (no scanners, non-risky
// tool, or scan clean). Returns (blocked=true, reason) when ANY scanner
// flags the input as blocked. Reason is a single human-readable string
// suitable for surfacing in the tool result so the LLM sees WHY it was
// denied and can re-plan.
func (a *Agent) preToolScan(toolName, args string) (bool, string) {
	// Protected-path gate runs FIRST and independently of the
	// scanner sweep — it doesn't need a scanner pattern, it just
	// needs the tool input. Stops the LLM from rewriting
	// ~/.overkill/memories/* (relationship arc, fingerprint, style)
	// or the append-only journals (failhypo, alerts, flight
	// recorder) via a generic write tool. The agent must mutate
	// these via typed tools that perform structured appends.
	if blocked, reason := checkProtectedPaths(toolName, args); blocked {
		return true, reason
	}

	if len(a.scanners) == 0 {
		return false, ""
	}
	payload := extractScanPayload(toolName, args)
	if payload == "" {
		return false, ""
	}
	for _, scanner := range a.scanners {
		result, err := scanner.Scan(payload)
		if err != nil || result == nil {
			continue
		}
		if result.Blocked {
			reason := scanner.Name() + " blocked tool execution"
			if len(result.Findings) > 0 && result.Findings[0].Description != "" {
				reason += ": " + result.Findings[0].Description
			}
			return true, reason
		}
	}
	return false, ""
}

// extractScanPayload pulls the user-controlled-looking string out of a
// tool-call argument blob so the scanners can evaluate it. Only known
// risky tools get scanned; the rest return "" and skip the check (we
// don't want to slow down every fs_read or git_status).
//
// The mapping is conservative — we scan WHAT the user would have typed
// at a shell. JSON unmarshal failure → fall back to scanning the raw
// args (better to over-scan than miss a malformed-but-real command).
func extractScanPayload(toolName, args string) string {
	switch toolName {
	case "shell", "pty_shell", "execute_command":
		var v struct {
			Command string `json:"command"`
			Cmd     string `json:"cmd"`
		}
		if err := json.Unmarshal([]byte(args), &v); err != nil {
			return strings.TrimSpace(args)
		}
		if v.Command != "" {
			return v.Command
		}
		return v.Cmd
	case "patch", "fs_write", "write_file":
		// The path is the dangerous part for these (path traversal,
		// writes outside repo). Scanners that watch for traversal
		// patterns get the path; ones that watch shell strings will
		// return clean on a bare path, which is fine.
		var v struct {
			Path string `json:"path"`
			File string `json:"file"`
		}
		if err := json.Unmarshal([]byte(args), &v); err != nil {
			return ""
		}
		if v.Path != "" {
			return v.Path
		}
		return v.File
	}
	return ""
}

// protectedSubdirs are paths UNDER ~/.overkill/ that the agent must
// not touch via generic write tools. The list is intentionally
// narrow: only state we've decided needs structural protection
// because losing it (or having it overwritten) costs the user real
// continuity. New protected dirs land here as we make them.
//
// Read access is NOT restricted — the agent can `cat ~/.overkill/
// memories/relationship-arc.json` to introspect itself; only writes
// are gated.
var protectedSubdirs = []string{
	"memories",            // relationship arc, fingerprint, style, coldstart
	"failed_hypotheses",   // append-only failhypo JSONL
	"journal",             // flight recorder + alerts
	"alerts",              // boot alert store
	"snapshots",           // §4.20 BadgerDB snapshots
	"receipts",            // §6.5 tool-receipt chain
	"plans",               // per-session plan store (mutate via plan_* tools)
	"learnings",           // append-only learnings stream (mutate via record_learning)
	"automation",          // alarms, SOPs, routines, flow state (Badger DB)
	"tasks",               // §8.3 cross-session task graph (mutate via task_* tools)
	"segments",            // §8.2 MemAgent segments (mutate via segment_* tools)
}

// protectedFiles are top-level ~/.overkill/ filenames that aren't
// in their own subdirectory but still need write protection.
// standing-orders.jsonl is the canonical example: agent must
// mutate it via standing_order_* tools, never via Write/Edit.
var protectedFiles = []string{
	"standing-orders.jsonl",
}

// writeToolPathExtractors maps a write-class tool name to the input
// fields whose values are filesystem paths to validate. The same
// tool may bind the path under different keys depending on how it
// was registered — we list all the variants we've seen.
//
// Tools not in this map are not write-class for the purposes of
// path protection. The catch-all "shell" tool is intentionally
// excluded — we don't try to parse arbitrary shell strings for
// embedded paths here; the regex scanners and the audit chain are
// the layers that catch malicious shell. Path protection's job is
// to stop the easy case: agent calls Write(path="~/.overkill/...").
var writeToolPathFields = map[string][]string{
	"Write":      {"file_path", "path", "file"},
	"Edit":       {"file_path", "path", "file"},
	"MultiEdit":  {"file_path", "path", "file"},
	"write_file": {"path", "file_path", "file"},
	"edit_file":  {"path", "file_path", "file"},
	"fs_write":   {"path", "file_path", "file"},
	"patch":      {"path", "file_path", "file"},
}

// checkProtectedPaths returns (true, reason) when toolName is a
// write-class tool whose target path resolves into a protected
// subdir under ~/.overkill/. The check tolerates relative paths
// and ~ expansion.
func checkProtectedPaths(toolName, args string) (bool, string) {
	fields, ok := writeToolPathFields[toolName]
	if !ok {
		return false, ""
	}
	if args == "" {
		return false, ""
	}
	// Parse loosely — write-tool inputs are always JSON objects.
	// If we can't parse, fall back to NOT blocking; the scanner
	// sweep below still runs.
	var raw map[string]any
	if err := json.Unmarshal([]byte(args), &raw); err != nil {
		return false, ""
	}
	for _, f := range fields {
		v, ok := raw[f]
		if !ok {
			continue
		}
		path, ok := v.(string)
		if !ok || path == "" {
			continue
		}
		if sub, hit := pathInProtectedSubdir(path); hit {
			return true, "protected-path: writes under ~/.overkill/" + sub +
				"/ are blocked. Use the typed tool (e.g. learn_record, " +
				"failhypo_search, journal_search) instead of editing this " +
				"file directly."
		}
	}
	return false, ""
}

// pathInProtectedSubdir resolves p against the user's home and
// reports whether the cleaned path sits under any protectedSubdir
// of ~/.overkill/. Returns ("memories", true) when the path is
// ~/.overkill/memories/relationship-arc.json; ("", false) otherwise.
func pathInProtectedSubdir(p string) (string, bool) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", false
	}
	// ~ expansion. If we can't resolve home, fall back to plain
	// substring match — better partial coverage than none.
	if strings.HasPrefix(p, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	// Absolute path resolution. For relative paths we just clean —
	// the agent's tools usually pass absolute paths anyway.
	clean := filepath.Clean(p)

	home, err := os.UserHomeDir()
	if err == nil {
		root := filepath.Join(home, ".overkill")
		for _, sub := range protectedSubdirs {
			needle := filepath.Join(root, sub)
			if clean == needle || strings.HasPrefix(clean, needle+string(filepath.Separator)) {
				return sub, true
			}
		}
		for _, fname := range protectedFiles {
			if clean == filepath.Join(root, fname) {
				return fname, true
			}
		}
	}
	// Fallback: structural match against the dir names. Catches
	// the case where home-dir resolution fails or the path is
	// relative-but-clearly-targeting (e.g.  ".overkill/memories/x").
	for _, sub := range protectedSubdirs {
		marker := ".overkill" + string(filepath.Separator) + sub
		if strings.Contains(clean, marker) {
			return sub, true
		}
	}
	for _, fname := range protectedFiles {
		marker := ".overkill" + string(filepath.Separator) + fname
		if strings.HasSuffix(clean, marker) {
			return fname, true
		}
	}
	return "", false
}

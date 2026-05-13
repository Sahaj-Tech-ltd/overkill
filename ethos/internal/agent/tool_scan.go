package agent

import (
	"encoding/json"
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

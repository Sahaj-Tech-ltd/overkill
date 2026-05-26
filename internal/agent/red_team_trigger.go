package agent

import (
	"encoding/json"
	"strings"
)

// red_team_trigger.go — auto-trigger CONDITIONS for the Wall 1
// Ouroboros review (master plan §6.5). The wall itself is expensive
// (separate-provider LLM call) so we don't INVOKE it automatically —
// we surface a recommendation via the `red_team_recommended` event
// when the conditions fire, and the agent / user can opt in by calling
// the wall_ouroboros tool.
//
// Trigger conditions per the plan:
//  1. Pre-ship on anything touching core systems (auth, crypto,
//     payments, data-loss paths).
//  2. Routing classifier complexity score above threshold AND task
//     touches core → auto-fire recommendation.
//
// Explicitly NOT triggered by:
//  - Agent self-reported confidence (MIRROR: models cannot
//    self-calibrate; circular).

// criticalPathHints matches file paths / commands that touch the
// systems §6.5 calls out. Conservative: false positives waste one
// recommendation (cheap); false negatives miss a critical review
// (expensive bug).
var criticalPathHints = []string{
	// Auth + identity.
	"auth", "authn", "authz", "login", "logout", "session",
	"jwt", "oauth", "saml", "token", "credential", "password",
	// Crypto + secrets.
	"crypto", "encrypt", "decrypt", "sign", "verify", "hash",
	"secret", "vault", "kms", "private_key", "private-key",
	// Payments + money.
	"payment", "billing", "checkout", "stripe", "invoice",
	"refund", "subscription", "charge",
	// Data-loss paths.
	"migrate", "migration", "schema", "drop_table", "truncate",
	"delete_user", "purge",
}

// criticalityCheck looks at a tool name + args JSON for signals that
// the operation touches a core system per §6.5. Returns (matched,
// reason). matched=false means no signal — caller skips the
// recommendation. Reason names the specific keyword + tool for
// observability.
func criticalityCheck(toolName, args string) (bool, string) {
	if !isWriteClassTool(toolName) {
		return false, ""
	}
	lower := strings.ToLower(args)
	// Cheap pass: bail before doing scan work when args are empty.
	if lower == "" {
		return false, ""
	}
	for _, hint := range criticalPathHints {
		if strings.Contains(lower, hint) {
			return true, "tool=" + toolName + " keyword=" + hint
		}
	}
	return false, ""
}

// isWriteClassTool returns true for tools that can change state in a
// way Red Team should review (file mutations, shell, patches).
// Read-only tools (fs.read, grep, journal_search, etc.) never trigger
// the recommendation.
func isWriteClassTool(name string) bool {
	switch name {
	case "shell", "pty_shell", "execute_command":
		return true
	case "patch", "fs_write", "write_file", "fs_edit", "fs_delete":
		return true
	}
	return false
}

// preToolRedTeamCheck inspects a pending tool call and emits a
// `red_team_recommended` event when the §6.5 trigger conditions fire.
// Non-blocking: the tool still executes. The event is informational —
// the user or the agent can react by invoking wall_ouroboros.
func (a *Agent) preToolRedTeamCheck(toolName string, args json.RawMessage) {
	matched, reason := criticalityCheck(toolName, string(args))
	if !matched {
		return
	}
	a.emit("red_team_recommended", map[string]any{
		"tool":       toolName,
		"reason":     reason,
		"session_id": a.sessionID,
		"hint":       "consider calling wall_ouroboros on this change before commit",
	})
}

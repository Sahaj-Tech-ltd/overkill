// Package agent — post-write verifier integration.
//
// The agent holds a PostWriteVerifier interface. After each tool
// dispatch in the streaming loop, if the tool succeeded AND the
// verifier is wired, we extract write paths from the tool input
// and run them through the verifier. Failures land as an extra
// "tool"-role message in history so the model sees them on the
// next turn.
//
// The interface lives in package agent (not internal/verify) so the
// agent doesn't import internal/verify directly — the wiring layer
// in cmd/overkill plugs the concrete registry in via SetVerifier.
// Keeps internal/agent dependency-free of the actual verifier
// implementations.
package agent

import (
	"context"
	"encoding/json"
)

// PostWriteVerifier is the minimal surface the agent calls after a
// successful write-class tool. The wiring layer (cmd/overkill)
// implements this in terms of internal/verify.
type PostWriteVerifier interface {
	// IsWriteTool reports whether toolName is a tool whose calls
	// should be verified. Lets the agent skip cheaply when the
	// tool isn't a write (no JSON parse, no registry lookup).
	IsWriteTool(toolName string) bool

	// VerifyToolCall runs verifiers against the paths named by the
	// tool's input. Returns a non-empty string when ANY verifier
	// failed — the string is the tool-message body the agent should
	// append to history. Empty string = nothing to surface.
	VerifyToolCall(ctx context.Context, toolName string, input json.RawMessage) string

	// ExtractWritePaths returns the absolute (or cwd-relative) paths
	// the tool wrote to. Used by the end-of-turn reward-hack audit
	// to collect every path touched across all write tools in this
	// turn, then run a cross-file check ('test changed without
	// code'). Empty when the tool isn't a write or the input has no
	// recognizable path keys.
	ExtractWritePaths(toolName string, input json.RawMessage) []string

	// AuditTurnPaths takes the deduplicated list of every path
	// written by every write-class tool call in a single agent turn
	// and returns a tool-message body summarising any reward-hack
	// findings (test files modified without their code). Empty
	// string = nothing to surface.
	AuditTurnPaths(paths []string) string
}

// SetPostWriteVerifier wires the verifier. nil disables. The agent
// is safe to use either way — when nil, the verifier hook is a
// no-op and the loop proceeds exactly as before.
func (a *Agent) SetPostWriteVerifier(v PostWriteVerifier) {
	if a == nil {
		return
	}
	a.verifierMu.Lock()
	a.postWriteVerifier = v
	a.verifierMu.Unlock()
}

// getPostWriteVerifier returns the currently wired verifier under
// read lock. nil is the legal "off" state.
func (a *Agent) getPostWriteVerifier() PostWriteVerifier {
	if a == nil {
		return nil
	}
	a.verifierMu.RLock()
	defer a.verifierMu.RUnlock()
	return a.postWriteVerifier
}

// (verifierMu + postWriteVerifier fields live on Agent itself in
// agent.go — declared here would create an init-order tangle. See
// agent.go for the field block.)

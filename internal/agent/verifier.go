// Package agent — post-write verifier integration.
//
// The agent holds a PostWriteVerifier interface. After each tool
// dispatch in either execution loop, if the tool succeeded AND the
// verifier is wired, we extract write paths from the tool input
// and run them through the verifier. Failures land as an extra
// system message in history so the model sees them on the
// next turn.
//
// The interface lives in package agent (not internal/verify) so the
// agent doesn't import internal/verify directly — the wiring layer
// in cmd/overkill plugs the concrete registry in via SetPostWriteVerifier.
// Keeps internal/agent dependency-free of the actual verifier
// implementations.
package agent

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
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

// dedupPaths returns paths with duplicates removed, preserving
// first-occurrence order. Used by the end-of-turn reward-hack audit
// to consolidate the path lists produced by multiple write tools
// that touched the same files (e.g. patch + edit on same path).
func dedupPaths(paths []string) []string {
	seen := make(map[string]bool, len(paths))
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// (verifierMu + postWriteVerifier fields live on Agent itself in
// agent.go — declared here would create an init-order tangle. See
// agent.go for the field block.)

// Verification state belongs to one invocation and travels through its context,
// so cached agents cannot carry pending failures into an unrelated turn.
type verificationKey struct{}
type pendingVerification struct{ call providers.ToolCall }
type verificationState struct {
	pending map[string]pendingVerification
}

func verificationContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, verificationKey{}, &verificationState{pending: make(map[string]pendingVerification)})
}

// verifyToolResults is the single post-dispatch hook used by Run and Stream.
// Feedback is a system message, never a fabricated tool_result ID.
func (a *Agent) verifyToolResults(ctx context.Context, calls []providers.ToolCall, results []ToolResult) {
	verifier := a.getPostWriteVerifier()
	if verifier == nil {
		return
	}
	state, _ := ctx.Value(verificationKey{}).(*verificationState)
	paths := []string{}
	for i, result := range results {
		if i >= len(calls) || result.Error != nil || !verifier.IsWriteTool(result.ToolName) {
			continue
		}
		var output struct {
			Error any `json:"error"`
		}
		if json.Unmarshal(result.Output, &output) == nil && output.Error != nil {
			continue
		}
		call := calls[i]
		input := json.RawMessage(call.Arguments)
		written := verifier.ExtractWritePaths(result.ToolName, input)
		if len(written) == 0 {
			continue
		}
		paths = append(paths, written...)
		note := verifier.VerifyToolCall(ctx, result.ToolName, input)
		if state != nil {
			for _, path := range written {
				if note == "" {
					delete(state.pending, path)
				} else {
					state.pending[path] = pendingVerification{call: call}
				}
			}
		}
		if note != "" {
			a.appendMessage(providers.Message{Role: "system", Content: note})
		}
	}
	if len(paths) > 0 {
		if note := verifier.AuditTurnPaths(dedupPaths(paths)); note != "" {
			a.appendMessage(providers.Message{Role: "system", Content: note})
		}
	}
}

// verificationCompletion rechecks only unresolved writes. If the model ignores
// feedback, terminal output stays explicitly blocked instead of reporting its
// unsupported success claim. A repair through another tool can still pass.
func (a *Agent) verificationCompletion(ctx context.Context) string {
	state, _ := ctx.Value(verificationKey{}).(*verificationState)
	verifier := a.getPostWriteVerifier()
	if state == nil || verifier == nil || len(state.pending) == 0 {
		return ""
	}
	seen := map[string]bool{}
	notes := []string{}
	keys := make([]string, 0, len(state.pending))
	for key := range state.pending {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		pending := state.pending[key]
		key := pending.call.Name + "\x00" + pending.call.Arguments
		if seen[key] {
			continue
		}
		seen[key] = true
		if note := verifier.VerifyToolCall(ctx, pending.call.Name, json.RawMessage(pending.call.Arguments)); note != "" {
			notes = append(notes, note)
		}
	}
	return strings.Join(notes, "\n\n")
}

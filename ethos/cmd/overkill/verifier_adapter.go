// Package main — bridge between agent.PostWriteVerifier and the
// internal/verify package. Lives in cmd/overkill so the agent
// package doesn't import internal/verify (keeps the agent's
// dependency graph thin).
package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/Sahaj-Tech-ltd/overkill/internal/verify"
)

// verifierAdapter implements agent.PostWriteVerifier by delegating
// to the verify package. The registry is created once at startup
// (DefaultRegistry) and shared across all tool calls.
type verifierAdapter struct {
	registry *verify.Registry
	cwd      string // for resolving relative paths
}

// newVerifierAdapter returns nil when verification is disabled via
// env (OVERKILL_NO_VERIFY=1 — useful for scripted CI where the
// agent shouldn't gate on build output) or when cwd can't be
// resolved. Otherwise returns a ready-to-wire adapter.
func newVerifierAdapter() *verifierAdapter {
	if os.Getenv("OVERKILL_NO_VERIFY") != "" {
		return nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil
	}
	return &verifierAdapter{
		registry: verify.DefaultRegistry(),
		cwd:      cwd,
	}
}

func (a *verifierAdapter) IsWriteTool(toolName string) bool {
	return verify.IsWriteTool(toolName)
}

// VerifyToolCall extracts paths from the tool input and runs the
// matching verifier on each. Returns the consolidated tool-message
// body when any verifier failed, or "" when everything passed (or
// the tool didn't write to any recognised path).
//
// Paths are resolved against the agent's cwd. We don't follow
// symlinks — a tool that wrote through a symlink resolves to the
// link path, not the real file, which keeps verifier output
// matching the path the agent originally referenced.
func (a *verifierAdapter) VerifyToolCall(ctx context.Context, toolName string, input json.RawMessage) string {
	paths := verify.ExtractWritePaths(toolName, input)
	if len(paths) == 0 {
		return ""
	}
	abs := make([]string, 0, len(paths))
	for _, p := range paths {
		if filepath.IsAbs(p) {
			abs = append(abs, filepath.Clean(p))
			continue
		}
		abs = append(abs, filepath.Join(a.cwd, p))
	}
	results := verify.VerifyPaths(ctx, a.registry, abs)
	// Trim the absolute path back to the cwd-relative form for the
	// note — easier for the model to reference and matches what
	// the user sees in their terminal.
	for i := range results {
		if rel, err := filepath.Rel(a.cwd, results[i].Path); err == nil && !strings.HasPrefix(rel, "..") {
			results[i].Path = rel
		}
	}
	return verify.FormatToolMessage(results)
}

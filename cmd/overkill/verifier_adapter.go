// Package main — bridge between agent.PostWriteVerifier and the
// internal/verify package. Lives in cmd/overkill so the agent
// package doesn't import internal/verify (keeps the agent's
// dependency graph thin).
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/verify"
)

// verifierAdapter implements agent.PostWriteVerifier by delegating
// to the verify package. The registry is created once at startup
// (DefaultRegistry) and shared across all tool calls.
type verifierAdapter struct {
	registry *verify.Registry
	cwd      string // immutable canonical session workspace
	lifetime context.Context
}

// newVerifierAdapter returns nil when verification is disabled via
// env (OVERKILL_NO_VERIFY=1). Invalid workspaces fail assembly instead of
// silently disabling checks. Each session supplies its own lifecycle context.
func newVerifierAdapter(lifetime context.Context, workspace string) (agent.PostWriteVerifier, error) {
	if os.Getenv("OVERKILL_NO_VERIFY") == "1" {
		return nil, nil
	}
	if lifetime == nil {
		return nil, fmt.Errorf("verifier: lifecycle context required")
	}
	if !filepath.IsAbs(workspace) {
		return nil, fmt.Errorf("verifier: absolute workspace required")
	}
	cwd, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		return nil, fmt.Errorf("verifier: workspace: %w", err)
	}
	info, err := os.Stat(cwd)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("verifier: workspace is not a directory")
	}
	return &verifierAdapter{registry: verify.DefaultRegistry(), cwd: cwd, lifetime: lifetime}, nil
}

func (a *verifierAdapter) IsWriteTool(toolName string) bool {
	return verify.IsWriteTool(toolName)
}

// ExtractWritePaths returns absolute paths the tool wrote to. The
// agent's end-of-turn reward-hack auditor uses these to collect the
// full per-turn picture across all write tools.
func (a *verifierAdapter) ExtractWritePaths(toolName string, input json.RawMessage) []string {
	paths := verify.ExtractWritePaths(toolName, input)
	if len(paths) == 0 {
		return nil
	}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if filepath.IsAbs(p) {
			out = append(out, filepath.Clean(p))
		} else {
			out = append(out, filepath.Join(a.cwd, p))
		}
	}
	return out
}

// AuditTurnPaths runs the reward-hack audit across every path
// written in a single agent turn. Paths are converted back to
// cwd-relative form for the rendered message so the model sees the
// same labels it referenced.
func (a *verifierAdapter) AuditTurnPaths(paths []string) string {
	findings := verify.AuditPaths(paths)
	if len(findings) == 0 {
		return ""
	}
	for i := range findings {
		if rel, err := filepath.Rel(a.cwd, findings[i].TestPath); err == nil && !strings.HasPrefix(rel, "..") {
			findings[i].TestPath = rel
		}
		if rel, err := filepath.Rel(a.cwd, findings[i].ExpectedCode); err == nil && !strings.HasPrefix(rel, "..") {
			findings[i].ExpectedCode = rel
		}
	}
	return verify.FormatRewardHackMessage(findings)
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
	paths := a.ExtractWritePaths(toolName, input)
	if len(paths) == 0 {
		return ""
	}
	checkCtx, cancel := context.WithCancel(ctx)
	if a.lifetime.Err() != nil {
		cancel()
	}
	defer cancel()
	stop := context.AfterFunc(a.lifetime, cancel)
	defer stop()
	results := []verify.VerifyResult{}
	root, err := os.OpenRoot(a.cwd)
	if err != nil {
		return "[verify] could not open workspace: " + err.Error()
	}
	defer root.Close()
	for _, path := range paths {
		verifier := a.registry.Lookup(path)
		if verifier == nil {
			continue
		}
		result := verify.VerifyResult{Path: path, Verifier: verifier.Name()}
		if err := checkCtx.Err(); err != nil {
			result.Detail = "verification canceled: " + err.Error()
			results = append(results, result)
			continue
		}
		name, err := filepath.Rel(a.cwd, path)
		if err != nil || name == ".." || strings.HasPrefix(name, ".."+string(os.PathSeparator)) {
			result.Detail = "verification target is outside the session workspace"
			results = append(results, result)
			continue
		}
		content, err := root.ReadFile(name)
		if err != nil {
			result.Detail = "could not verify file: " + err.Error()
			results = append(results, result)
			continue
		}
		real, err := filepath.EvalSymlinks(path)
		if err != nil {
			result.Detail = err.Error()
			results = append(results, result)
			continue
		}
		relative, err := filepath.Rel(a.cwd, real)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
			result.Detail = "verification target resolves outside the session workspace"
			results = append(results, result)
			continue
		}
		bounded, finish := context.WithTimeout(checkCtx, verifier.Timeout())
		ok, detail, skipped := invokeObservedVerifier(bounded, verifier, real, name, content)
		if err := bounded.Err(); err != nil {
			ok = false
			skipped = true
			detail = err.Error()
		}
		finish()
		if skipped {
			ok = false
			detail = "verification could not complete: " + detail
		}
		result.Ok = ok
		result.Detail = detail
		result.Path = name
		results = append(results, result)
	}
	return verify.FormatToolMessage(results)
}

// Always close the observation on return or panic; checker failures must not
// leave a permanently running display. Existing panic propagation is preserved.
func invokeObservedVerifier(ctx context.Context, checker verify.Verifier, path, displayPath string, content []byte) (ok bool, detail string, skipped bool) {
	observed := agent.StartVerification(ctx, checker.Name(), displayPath)
	status := "failed"
	detail = "verifier interrupted"
	defer func() {
		if ctx.Err() != nil {
			status = "canceled"
			detail = ctx.Err().Error()
		}
		observed(status, detail)
	}()
	ok, detail, skipped = checker.Verify(ctx, path, content)
	if ok {
		status = "passed"
	}
	if skipped {
		status = "skipped"
	}
	return
}

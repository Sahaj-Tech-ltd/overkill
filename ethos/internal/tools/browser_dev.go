// Package tools — browser_dev tool wraps the external `dev-browser` CLI
// (https://github.com/SawyerHood/dev-browser) so the agent can drive a
// long-running developer browser session via small JS scripts piped in
// via stdin (master plan §7.3).
//
// Why dev-browser as a third option (alongside chromedp + WebSocket browser):
//   - chromedp owns the headless workflow.
//   - dev-browser keeps a stateful, named-page session the user can see.
//   - It exposes Playwright's full surface plus snapshotForAI() — easier
//     for the model to reason about real pages.
//
// The tool degrades cleanly when the binary is missing.
package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// BrowserDevTool runs a JS snippet through `dev-browser`.
type BrowserDevTool struct {
	binary string // resolved path; empty when not installed
}

// NewBrowserDevTool resolves the binary on PATH. Returns a tool that returns
// a clear error from Execute when dev-browser isn't installed; callers can
// register it unconditionally.
func NewBrowserDevTool() *BrowserDevTool {
	bin, _ := exec.LookPath("dev-browser")
	return &BrowserDevTool{binary: bin}
}

func (t *BrowserDevTool) Name() string { return "browser_dev" }

type browserDevInput struct {
	Script         string `json:"script"`
	Connect        bool   `json:"connect,omitempty"`         // pass --connect (attach to existing session)
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"` // default 60s
}

type browserDevOutput struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr,omitempty"`
	ExitCode int    `json:"exit_code"`
	Connect  bool   `json:"connect"`
}

func (t *BrowserDevTool) Execute(ctx context.Context, in json.RawMessage) (json.RawMessage, error) {
	if t.binary == "" {
		return errorJSON("browser_dev: dev-browser binary not found on PATH (install: https://github.com/SawyerHood/dev-browser)"), nil
	}
	var req browserDevInput
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, fmt.Errorf("browser_dev: %w", err)
	}
	if strings.TrimSpace(req.Script) == "" {
		return errorJSON("script is required"), nil
	}
	timeout := time.Duration(req.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	args := []string{}
	if req.Connect {
		args = append(args, "--connect")
	}
	cmd := exec.CommandContext(cctx, t.binary, args...)
	cmd.Stdin = bytes.NewReader([]byte(req.Script))
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	exit := 0
	if cmd.ProcessState != nil {
		exit = cmd.ProcessState.ExitCode()
	}
	out := browserDevOutput{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exit,
		Connect:  req.Connect,
	}
	if runErr != nil && cmd.ProcessState == nil {
		// Process never started (binary unusable, ctx cancelled).
		return errorJSON(fmt.Sprintf("browser_dev: %v", runErr)), nil
	}
	raw, _ := json.Marshal(out)
	return raw, nil
}

// IsAvailable reports whether dev-browser is on PATH. Useful for /browser
// status to show the third option as available/unavailable.
func (t *BrowserDevTool) IsAvailable() bool { return t.binary != "" }

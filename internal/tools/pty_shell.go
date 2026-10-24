package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/processrunner"
	"github.com/Sahaj-Tech-ltd/overkill/internal/pty"
	"github.com/Sahaj-Tech-ltd/overkill/internal/security"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tools/bash"
)

// PTYShellTool runs a command inside a pseudo-terminal so that progress bars,
// prompts, and other TTY-aware output behave the way the user expects.
type PTYShellTool struct {
	maxTimeout time.Duration
	cwd        string
	// scanner mirrors ShellTool: defense-in-depth check on the raw command
	// before exec. The agent-level preToolScan also runs for agent
	// dispatch, but a scanner here catches direct callers (plugins,
	// subagents) that bypass the agent loop.
	scanner *security.CommandScanner
	// bashValidate enables shell-specific security classification
	// (the validator chain from internal/tools/bash/). When nil,
	// only the generic CommandScanner runs. Set via WithBashSecurity().
	bashValidate func(string) bash.Result
}

// BashSecurityOption controls whether the bash validator chain runs
// inside PTYShellTool.Execute. Pass true to enable.
type BashSecurityOption bool

// WithBashSecurity enables or disables shell-specific security validation.
// When enabled, the bash validator chain runs before the generic CommandScanner.
func WithBashSecurity(enabled BashSecurityOption) func(*PTYShellTool) {
	return func(t *PTYShellTool) {
		if enabled {
			t.bashValidate = bash.Validate
		}
	}
}

func NewPTYShellTool(cwd string, opts ...func(*PTYShellTool)) *PTYShellTool {
	t := &PTYShellTool{
		maxTimeout: 900 * time.Second,
		cwd:        cwd,
		scanner:    security.NewCommandScanner(security.WithProjectPath(cwd)),
	}
	for _, o := range opts {
		o(t)
	}
	return t
}

func (t *PTYShellTool) Name() string { return "pty_shell" }

func (t *PTYShellTool) Available() bool { return pty.Supported() }

func (t *PTYShellTool) Description() string {
	return "Run one bounded command with real terminal I/O using " + pty.ShellName() + ". The command is closed when it finishes, times out or is cancelled. Use terminal for work that must survive tool calls."
}

type ptyShellInput struct {
	Command        string `json:"command"`
	Cwd            string `json:"cwd"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

type ptyShellOutput struct {
	OutputBytes     int64  `json:"output_bytes"`
	OutputTruncated bool   `json:"output_truncated,omitempty"`
	ExitCode        int    `json:"exit_code"`
	Output          string `json:"output"`
	TimedOut        bool   `json:"timed_out"`
}

func (t *PTYShellTool) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	if !t.Available() {
		return nil, fmt.Errorf("pty_shell: native interactive terminals are unavailable on this host")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var in ptyShellInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("pty_shell: %w", err)
	}
	if strings.TrimSpace(in.Command) == "" {
		return nil, fmt.Errorf("pty_shell: command is required")
	}

	// Bash security validator chain (port from Claude Code):
	// runs before the generic CommandScanner for shell-specific checks.
	if t.bashValidate != nil {
		r := t.bashValidate(in.Command)
		switch r.Behavior {
		case bash.Deny:
			return nil, fmt.Errorf("pty_shell: blocked: %s (%s)", r.Message, r.Reason)
		case bash.Ask:
			// The agent loop's checkToolApproval will prompt the user.
			// We pass through here — the agent sees the Ask result and
			// surfaces it in the approval dialog.
		}
	}

	// Defense-in-depth scan, identical to ShellTool. Mismatched gates here
	// were the original concern: agent-level scan covers agent dispatch,
	// but a plugin or subagent calling PTYShellTool directly previously
	// got zero filtering.
	if t.scanner != nil {
		res, err := t.scanner.Scan(in.Command)
		if err != nil {
			// Scanner error means we couldn't verify safety — treat as a block
			// (fail-closed, mirroring shell.go behaviour).
			return nil, fmt.Errorf("pty_shell: scanner error: %w", err)
		}
		if res != nil && res.Blocked {
			reason := "blocked by command scanner"
			if len(res.Findings) > 0 {
				reason = res.Findings[0].Description
			}
			return nil, fmt.Errorf("pty_shell: %s", reason)
		}
	}

	timeout := estimateTimeout(in.Command)
	if in.TimeoutSeconds > 0 {
		timeout = time.Duration(in.TimeoutSeconds) * time.Second
	}
	if timeout > t.maxTimeout {
		timeout = t.maxTimeout
	}

	cwd := in.Cwd
	if cwd == "" {
		cwd = t.cwd
	}
	// Mirror ShellTool's containment: when a workspace root (t.cwd) is
	// configured and the caller is overriding with a different cwd,
	// require the override to stay inside the workspace. The old code
	// honoured any "cwd" the LLM picked, including "/etc", giving the
	// pty_shell tool a free escape from the project root that the
	// non-pty ShellTool didn't have.
	if in.Cwd != "" && t.cwd != "" {
		base, baseErr := filepath.Abs(t.cwd)
		dir, dirErr := filepath.Abs(in.Cwd)
		if baseErr != nil || dirErr != nil {
			return nil, fmt.Errorf("pty_shell: resolve cwd: base=%v dir=%v", baseErr, dirErr)
		}
		rel, rerr := filepath.Rel(filepath.Clean(base), filepath.Clean(dir))
		if rerr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("pty_shell: cwd %q is outside workspace %q", in.Cwd, t.cwd)
		}
	}

	cmd := pty.ShellCommand(in.Command)
	if cwd != "" {
		cmd.Dir = cwd
	}

	sess := pty.New()
	if err := sess.Start(cmd); err != nil {
		return nil, fmt.Errorf("pty_shell: start: %w", err)
	}
	defer sess.Close()

	// Default 80x24 — matches a typical terminal so tools that branch on
	// COLUMNS produce sensible output.
	_ = sess.Resize(24, 80)

	capture := processrunner.NewOutputCapture(1024 * 1024)
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		buf := make([]byte, 4096)
		for {
			n, err := sess.Read(buf)
			if n > 0 {
				_, _ = capture.Write(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	doneCh := make(chan int, 1)
	go func() { code, _ := sess.WaitExit(); doneCh <- code }()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	code, timedOut := 0, false
	select {
	case <-ctx.Done():
		code = -1
	case <-timer.C:
		code = -1
		timedOut = true
	case code = <-doneCh:
		select {
		case <-readerDone:
		case <-time.After(500 * time.Millisecond):
		}
	}
	_ = sess.Close()
	<-readerDone
	text, total, truncated := capture.Snapshot()
	output, err := json.Marshal(ptyShellOutput{ExitCode: code, TimedOut: timedOut, Output: text, OutputBytes: total, OutputTruncated: truncated})
	// Preserve the one-command tool protocol: cancellation is exit -1.
	// The owning agent still observes its cancelled context.
	if ctx.Err() != nil {
		return output, err
	}
	return output, err
}

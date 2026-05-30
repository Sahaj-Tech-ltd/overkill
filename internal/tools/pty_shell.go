package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

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
		maxTimeout: 5 * time.Minute,
		cwd:        cwd,
		scanner:    security.NewCommandScanner(security.WithProjectPath(cwd)),
	}
	for _, o := range opts {
		o(t)
	}
	return t
}

func (t *PTYShellTool) Name() string { return "pty_shell" }

type ptyShellInput struct {
	Command        string `json:"command"`
	Cwd            string `json:"cwd"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

type ptyShellOutput struct {
	ExitCode int    `json:"exit_code"`
	Output   string `json:"output"`
	TimedOut bool   `json:"timed_out"`
}

func (t *PTYShellTool) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
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
		if err == nil && res != nil && res.Blocked {
			reason := "blocked by command scanner"
			if len(res.Findings) > 0 {
				reason = res.Findings[0].Description
			}
			return nil, fmt.Errorf("pty_shell: %s", reason)
		}
	}

	timeout := 60 * time.Second
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

	cmd := exec.CommandContext(ctx, "sh", "-c", in.Command)
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

	type readResult struct {
		out string
		err error
	}
	rc := make(chan readResult, 1)
	go func() {
		var sb strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := sess.Read(buf)
			if n > 0 {
				sb.Write(buf[:n])
			}
			if err != nil {
				rc <- readResult{out: sb.String(), err: err}
				return
			}
		}
	}()

	doneCh := make(chan int, 1)
	go func() {
		code, _ := sess.WaitExit()
		doneCh <- code
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		_ = sess.Close()
		<-rc
		return json.Marshal(ptyShellOutput{ExitCode: -1, TimedOut: false, Output: ""})
	case <-timer.C:
		_ = sess.Close()
		res := <-rc
		return json.Marshal(ptyShellOutput{ExitCode: -1, TimedOut: true, Output: res.out})
	case code := <-doneCh:
		var out string
		select {
		case res := <-rc:
			out = res.out
		case <-time.After(500 * time.Millisecond):
			// Reader didn't see EOF yet (race); accept partial.
		}
		return json.Marshal(ptyShellOutput{ExitCode: code, Output: out})
	}
}

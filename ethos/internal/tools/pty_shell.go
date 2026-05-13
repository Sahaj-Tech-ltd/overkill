package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/pty"
)

// PTYShellTool runs a command inside a pseudo-terminal so that progress bars,
// prompts, and other TTY-aware output behave the way the user expects.
type PTYShellTool struct {
	maxTimeout time.Duration
	cwd        string
}

func NewPTYShellTool(cwd string) *PTYShellTool {
	return &PTYShellTool{maxTimeout: 5 * time.Minute, cwd: cwd}
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

	cmd := exec.Command("sh", "-c", in.Command)
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
		_ = io.EOF
		return json.Marshal(ptyShellOutput{ExitCode: code, Output: out})
	}
}

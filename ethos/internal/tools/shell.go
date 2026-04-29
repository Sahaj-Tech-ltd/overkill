package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"time"
)

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

type ShellTool struct {
	maxTimeout       time.Duration
	defaultWorkingDir string
}

type ShellInput struct {
	Command        string            `json:"command"`
	TimeoutSeconds int               `json:"timeout_seconds"`
	WorkingDir     string            `json:"working_dir"`
	Env            map[string]string `json:"env"`
}

type ShellOutput struct {
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	TimedOut bool   `json:"timed_out"`
}

func NewShellTool(opts ...func(*ShellTool)) *ShellTool {
	t := &ShellTool{
		maxTimeout:       120 * time.Second,
		defaultWorkingDir: "",
	}
	for _, opt := range opts {
		opt(t)
	}
	return t
}

func (s *ShellTool) Name() string {
	return "shell"
}

func (s *ShellTool) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	var in ShellInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("shell: %w", err)
	}

	if in.Command == "" {
		return nil, fmt.Errorf("shell: command is required")
	}

	timeout := 30 * time.Second
	if in.TimeoutSeconds > 0 {
		timeout = time.Duration(in.TimeoutSeconds) * time.Second
	}
	if timeout > s.maxTimeout {
		timeout = s.maxTimeout
	}

	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "sh", "-c", in.Command)

	if in.WorkingDir != "" {
		cmd.Dir = in.WorkingDir
	} else if s.defaultWorkingDir != "" {
		cmd.Dir = s.defaultWorkingDir
	}

	if len(in.Env) > 0 {
		cmd.Env = append(cmd.Environ(), envSlice(in.Env)...)
	}

	var combined bytes.Buffer
	cmd.Stdout = &combined
	cmd.Stderr = &combined

	err := cmd.Run()

	output := ShellOutput{
		ExitCode: 0,
		Stdout:   ansiRe.ReplaceAllString(combined.String(), ""),
		Stderr:   "",
		TimedOut: false,
	}

	if err != nil {
		if cmdCtx.Err() == context.DeadlineExceeded {
			output.TimedOut = true
			output.ExitCode = -1
			output.Stderr = fmt.Sprintf("command timed out after %s", timeout)
		} else {
			if exitErr, ok := err.(*exec.ExitError); ok {
				output.ExitCode = exitErr.ExitCode()
			} else {
				output.ExitCode = -1
			}
		}
	}

	raw, err := json.Marshal(output)
	if err != nil {
		return nil, fmt.Errorf("shell: %w", err)
	}
	return raw, nil
}

func envSlice(env map[string]string) []string {
	s := make([]string, 0, len(env))
	for k, v := range env {
		s = append(s, k+"="+v)
	}
	return s
}

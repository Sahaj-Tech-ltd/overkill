package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

type GitTool struct {
	workingDir string
	timeout    time.Duration
}

type GitInput struct {
	Action   string `json:"action"`
	Staged   bool   `json:"staged"`
	Stat     bool   `json:"stat"`
	Count    int    `json:"count"`
	Paths    []string `json:"paths"`
	Message  string `json:"message"`
	Ref      string `json:"ref"`
	StashAction string `json:"stash_action"`
}

func NewGitTool(workingDir string) *GitTool {
	return &GitTool{
		workingDir: workingDir,
		timeout:    30 * time.Second,
	}
}

func (g *GitTool) Name() string {
	return "git"
}

func (g *GitTool) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	var in GitInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("git: %w", err)
	}

	switch in.Action {
	case "status":
		return g.runGit(ctx, "status", "--porcelain")
	case "diff":
		return g.diff(ctx, &in)
	case "log":
		return g.log(ctx, &in)
	case "add":
		return g.add(ctx, &in)
	case "commit":
		return g.commit(ctx, &in)
	case "push":
		return g.runGit(ctx, "push")
	case "reset":
		return g.reset(ctx, &in)
	case "stash":
		return g.stash(ctx, &in)
	default:
		return nil, fmt.Errorf("git: unknown action %q", in.Action)
	}
}

func (g *GitTool) runGit(ctx context.Context, args ...string) (json.RawMessage, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, g.timeout)
	defer cancel()

	shell := NewShellTool(func(s *ShellTool) {
		s.defaultWorkingDir = g.workingDir
		s.maxTimeout = g.timeout + 5*time.Second
	})

	gitArgs := append([]string{"git"}, args...)
	quoted := make([]string, len(gitArgs))
	for i, a := range gitArgs {
		quoted[i] = shellQuote(a)
	}
	input, _ := json.Marshal(ShellInput{
		Command:        strings.Join(quoted, " "),
		TimeoutSeconds: int(g.timeout.Seconds()) + 5,
		WorkingDir:     g.workingDir,
	})

	out, err := shell.Execute(cmdCtx, input)
	if err != nil {
		return nil, fmt.Errorf("git %s: %w", args[0], err)
	}

	var shellOut ShellOutput
	if err := json.Unmarshal(out, &shellOut); err != nil {
		return nil, fmt.Errorf("git %s: %w", args[0], err)
	}

	result := ToolResult{
		Output:  shellOut.Stdout,
		Success: shellOut.ExitCode == 0,
	}
	if !result.Success {
		result.Error = fmt.Sprintf("exit code %d", shellOut.ExitCode)
	}

	raw, _ := json.Marshal(result)
	return raw, nil
}

func (g *GitTool) diff(ctx context.Context, in *GitInput) (json.RawMessage, error) {
	args := []string{"diff"}
	if in.Staged {
		args = append(args, "--staged")
	}
	if in.Stat {
		args = append(args, "--stat")
	}
	return g.runGit(ctx, args...)
}

func (g *GitTool) log(ctx context.Context, in *GitInput) (json.RawMessage, error) {
	count := 10
	if in.Count > 0 {
		count = in.Count
	}
	args := []string{"log", "--oneline", fmt.Sprintf("-n%d", count)}
	return g.runGit(ctx, args...)
}

func (g *GitTool) add(ctx context.Context, in *GitInput) (json.RawMessage, error) {
	args := []string{"add"}
	args = append(args, in.Paths...)
	return g.runGit(ctx, args...)
}

func (g *GitTool) commit(ctx context.Context, in *GitInput) (json.RawMessage, error) {
	if in.Message == "" {
		return nil, fmt.Errorf("git commit: message is required")
	}
	args := []string{"commit", "-m", in.Message}
	return g.runGit(ctx, args...)
}

func (g *GitTool) reset(ctx context.Context, in *GitInput) (json.RawMessage, error) {
	args := []string{"reset", "--hard"}
	if in.Ref != "" {
		args = append(args, in.Ref)
	}
	return g.runGit(ctx, args...)
}

func (g *GitTool) stash(ctx context.Context, in *GitInput) (json.RawMessage, error) {
	switch in.StashAction {
	case "push", "":
		return g.runGit(ctx, "stash", "push")
	case "pop":
		return g.runGit(ctx, "stash", "pop")
	case "list":
		return g.runGit(ctx, "stash", "list")
	default:
		return nil, fmt.Errorf("git stash: unknown stash action %q", in.StashAction)
	}
}

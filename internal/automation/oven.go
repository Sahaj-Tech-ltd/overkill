// Package automation — OvenActions runtime dispatch (master plan §8.4).
//
// When a Board-based task transitions to "Done" status, any configured
// OvenActions fire automatically. This file provides the dispatch loop
// that executes each action in order, stopping on the first failure
// (fail-fast) or continuing based on the action's configuration.
//
// Supported actions:
//   - git_push:      git push the current branch
//   - open_pr:       open a pull request via GitHub CLI
//   - deploy:        trigger a deployment via a shell command
//   - notify_slack:  send a Slack notification
package automation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
)

// TaskInfo carries the context needed by OvenActions at dispatch time.
type TaskInfo struct {
	TaskID       string
	TaskName     string
	BranchName   string // git branch name
	RepoPath     string // local repo path
	PRTitle      string
	PRBody       string
	Result       string // the task output/result
	SlackWebhook string // override Slack webhook URL (env: SLACK_WEBHOOK_URL)
}

// ExecuteOvenActions runs each configured oven action in order. Actions are
// executed sequentially; by default execution stops on the first error
// (fail-fast). Callers should wire this into the task-completion path
// (e.g., Ledger terminal sink or board status transition handler).
//
// Errors from individual actions are collected and returned. A nil error
// means all actions succeeded.
func ExecuteOvenActions(ctx context.Context, actions []config.BoardOvenAction, info TaskInfo) error {
	if len(actions) == 0 {
		return nil
	}

	var errs []string
	for i, a := range actions {
		select {
		case <-ctx.Done():
			return fmt.Errorf("oven: cancelled after %d/%d actions: %w", i, len(actions), ctx.Err())
		default:
		}

		handler, ok := actionHandlers[a.Kind]
		if !ok {
			errs = append(errs, fmt.Sprintf("oven: unknown action kind %q", a.Kind))
			continue
		}

		if err := handler(ctx, a, info); err != nil {
			errs = append(errs, fmt.Sprintf("oven: action %d/%d (%s): %v", i+1, len(actions), a.Kind, err))
			// Fail-fast by default; auto_fire overrides can be added
			// per-action if needed in the future.
			break
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "\n"))
	}
	return nil
}

// actionHandlers maps action kind strings to their implementations.
var actionHandlers = map[string]func(context.Context, config.BoardOvenAction, TaskInfo) error{
	"git_push":     ovenGitPush,
	"open_pr":      ovenOpenPR,
	"deploy":       ovenDeploy,
	"notify_slack": ovenNotifySlack,
}

// ovenGitPush pushes the current branch to origin.
func ovenGitPush(ctx context.Context, a config.BoardOvenAction, info TaskInfo) error {
	remote := stringArg(a.Args, "remote", "origin")
	branch := stringArg(a.Args, "branch", info.BranchName)
	if branch == "" {
		branch = currentGitBranch(ctx, info.RepoPath)
	}
	if branch == "" {
		return fmt.Errorf("git_push: no branch specified and could not detect current branch")
	}

	cmd := exec.CommandContext(ctx, "git", "push", remote, branch)
	cmd.Dir = info.RepoPath
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git_push: %s: %w\n%s", strings.TrimSpace(string(out)), err, string(out))
	}
	return nil
}

// ovenOpenPR opens a pull request using the GitHub CLI (gh).
func ovenOpenPR(ctx context.Context, a config.BoardOvenAction, info TaskInfo) error {
	title := stringArg(a.Args, "title", info.PRTitle)
	body := stringArg(a.Args, "body", info.PRBody)
	base := stringArg(a.Args, "base", "main")

	if title == "" {
		title = info.TaskName
	}
	if title == "" {
		return fmt.Errorf("open_pr: no PR title available")
	}

	args := []string{"pr", "create", "--title", title, "--base", base}
	if body != "" {
		args = append(args, "--body", body)
	}

	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Dir = info.RepoPath
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("open_pr: %s: %w\n%s", strings.TrimSpace(string(out)), err, string(out))
	}
	return nil
}

// ovenDeploy runs a deployment command configured in the action args.
// The "command" arg is required — it's the shell command to execute.
func ovenDeploy(ctx context.Context, a config.BoardOvenAction, info TaskInfo) error {
	command := stringArg(a.Args, "command", "")
	if command == "" {
		return fmt.Errorf("deploy: missing required arg \"command\"")
	}

	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = info.RepoPath
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("deploy: %s: %w\n%s", strings.TrimSpace(string(out)), err, string(out))
	}
	return nil
}

// ovenNotifySlack sends a notification to a Slack webhook.
func ovenNotifySlack(ctx context.Context, a config.BoardOvenAction, info TaskInfo) error {
	webhookURL := stringArg(a.Args, "webhook_url", info.SlackWebhook)
	if webhookURL == "" {
		return fmt.Errorf("notify_slack: no webhook URL configured")
	}

	msg := stringArg(a.Args, "message", "")
	if msg == "" {
		msg = fmt.Sprintf("Task *%s* completed — %s", info.TaskName, info.Result)
	}

	type slackPayload struct {
		Text string `json:"text"`
	}
	payloadBytes, err := json.Marshal(slackPayload{Text: msg})
	if err != nil {
		return fmt.Errorf("notify_slack: marshal payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(payloadBytes))
	if err != nil {
		return fmt.Errorf("notify_slack: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("notify_slack: post: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("notify_slack: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return nil
}

// ── helpers ──────────────────────────────────────────────────────────────

func stringArg(args map[string]any, key, defaultVal string) string {
	if args == nil {
		return defaultVal
	}
	v, ok := args[key]
	if !ok {
		return defaultVal
	}
	s, ok := v.(string)
	if !ok {
		return defaultVal
	}
	return s
}

// currentGitBranch returns the current git branch name in the given repo.
func currentGitBranch(ctx context.Context, repoPath string) string {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// WrapTerminalSinkWithOven returns a TerminalSink that runs first the
// original sink, then executes the configured OvenActions for completed
// tasks. Failures in oven actions fire log.Printf but never suppress the
// original sink, so the user always gets their completion notification.
//
// The sink wraps the existing Ledger.TerminalSink; daemon wiring calls
// this at startup with the BoardUserConfig's OvenActions.
func WrapTerminalSinkWithOven(inner TerminalSink, actions []config.BoardOvenAction) TerminalSink {
	if len(actions) == 0 {
		return inner
	}
	return func(t LedgerTask) {
		// Fire the inner sink first (always).
		if inner != nil {
			inner(t)
		}

		// Only run oven actions for completed tasks.
		if t.State != TaskCompleted {
			return
		}

		info := TaskInfo{
			TaskID:   t.ID,
			TaskName: t.Name,
			Result:   t.Result,
		}

		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()

		if err := ExecuteOvenActions(ctx, actions, info); err != nil {
			log.Printf("oven: actions failed for task %q: %v", t.Name, err)
		}
	}
}

// git-stats plugin: surfaces lightweight git context to the agent. Adds
// a git_stats tool, subscribes to compact (and stashes a snapshot), and
// registers a context provider that injects `git status --short` before
// every prompt.
package main

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"time"

	sdk "github.com/Sahaj-Tech-ltd/overkill/examples/plugins/sdk-go"
)

func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

func gitStats(ctx context.Context, path string) map[string]any {
	since := time.Now().Format("2006-01-02") + "T00:00:00"
	commits, _ := runGit(ctx, path, "log", "--since="+since, "--oneline")
	files, _ := runGit(ctx, path, "diff", "--name-only", "--since="+since)
	authors, _ := runGit(ctx, path, "log", "--since="+since, "--format=%an")
	authorCounts := map[string]int{}
	for _, a := range strings.Split(authors, "\n") {
		if a == "" {
			continue
		}
		authorCounts[a]++
	}
	type ac struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}
	var top []ac
	for n, c := range authorCounts {
		top = append(top, ac{Name: n, Count: c})
	}
	return map[string]any{
		"commits_today":       countLines(commits),
		"files_changed_today": countLines(files),
		"top_authors":         top,
	}
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	return len(strings.Split(s, "\n"))
}

func main() {
	p := sdk.New(sdk.Manifest{
		Name:        "git-stats",
		Version:     "0.1.0",
		Description: "Adds git_stats tool, /standup snapshot on compact, and pre-prompt git status context",
		Permissions: sdk.Permissions{
			Events: []string{"compact"},
		},
	})

	p.RegisterTool(sdk.ToolDecl{
		Name:        "git_stats",
		Description: "Today's commits, files changed, and top authors for a repo",
	})

	p.OnTool("git_stats", func(ctx context.Context, args json.RawMessage) (any, error) {
		var in struct {
			Path string `json:"path"`
		}
		_ = json.Unmarshal(args, &in)
		return gitStats(ctx, in.Path), nil
	})

	p.Subscribe("compact")
	p.OnEvent(func(payload json.RawMessage) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		// Best-effort: stash whatever is currently dirty so the user can
		// recover state even though the conversation just got compacted.
		_, _ = runGit(ctx, "", "stash", "push", "-u", "-m", "overkill pre-compact "+time.Now().Format(time.RFC3339))
	})

	p.OnContext(func(ctx context.Context, prompt, sessionID string) []sdk.ContextSnippet {
		out, err := runGit(ctx, "", "status", "--short")
		if err != nil || out == "" {
			return nil
		}
		return []sdk.ContextSnippet{{
			Title:   "git status",
			Content: out,
		}}
	})

	_ = p.Run()
}

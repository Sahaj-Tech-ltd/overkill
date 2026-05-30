package chips

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/prompt"
)

// GitDiffChip reports the git diff summary via `git diff --stat HEAD`.
// Returns a compact overview of uncommitted changes.
type GitDiffChip struct {
	enabled bool
}

// NewGitDiffChip returns a GitDiffChip that starts enabled.
func NewGitDiffChip() *GitDiffChip {
	return &GitDiffChip{enabled: true}
}

// Kind implements prompt.Chip.
func (c *GitDiffChip) Kind() string { return "git_diff" }

// Title implements prompt.Chip.
func (c *GitDiffChip) Title() string { return "Git Diff" }

// Value implements prompt.Chip. Runs git diff --stat HEAD with a
// 5-second timeout. Returns empty string if git is unavailable or
// there are no changes.
func (c *GitDiffChip) Value(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", "diff", "--stat", "HEAD")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = nil

	if err := cmd.Run(); err != nil {
		return "", err
	}
	result := strings.TrimSpace(out.String())
	if result == "" {
		// No uncommitted changes — normal, not an error.
		return "", nil
	}
	return result, nil
}

// RefreshPolicy implements prompt.Chip.
func (c *GitDiffChip) RefreshPolicy() prompt.RefreshPolicy { return prompt.EveryTurn }

// Enabled implements prompt.Chip.
func (c *GitDiffChip) Enabled() bool { return c.enabled }

// SetEnabled toggles this chip on or off.
func (c *GitDiffChip) SetEnabled(v bool) { c.enabled = v }

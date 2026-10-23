package chips

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/prompt"
)

// GitBranchChip reports the current git branch name via
// `git rev-parse --abbrev-ref HEAD`.
type GitBranchChip struct {
	enabled    bool
	workingDir string
}

// NewGitBranchChip returns a GitBranchChip that starts enabled.
func NewGitBranchChip(workingDir ...string) *GitBranchChip {
	directory := ""
	if len(workingDir) > 0 {
		directory = workingDir[0]
	}
	return &GitBranchChip{enabled: true, workingDir: directory}
}

// Kind implements prompt.Chip.
func (c *GitBranchChip) Kind() string { return "git_branch" }

// Title implements prompt.Chip.
func (c *GitBranchChip) Title() string { return "Git Branch" }

// Value implements prompt.Chip. Runs git rev-parse --abbrev-ref HEAD
// with a 5-second timeout. Returns empty string if git is unavailable
// or the command fails.
func (c *GitBranchChip) Value(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = c.workingDir
	var out bytes.Buffer
	cmd.Stdout = &out
	// Discard stderr — git errors (e.g. no repo) should be silent.
	cmd.Stderr = nil

	if err := cmd.Run(); err != nil {
		return "", err
	}
	return strings.TrimSpace(out.String()), nil
}

// RefreshPolicy implements prompt.Chip. Git branch changes infrequently
// during a session, so OnChange avoids redundant git calls.
func (c *GitBranchChip) RefreshPolicy() prompt.RefreshPolicy { return prompt.OnChange }

// Enabled implements prompt.Chip.
func (c *GitBranchChip) Enabled() bool { return c.enabled }

// SetEnabled toggles this chip on or off.
func (c *GitBranchChip) SetEnabled(v bool) { c.enabled = v }

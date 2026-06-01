// Package chips provides built-in context chips for the Overkill agent.
// Each chip contributes a single piece of dynamic context to the system
// prompt — directory, git branch, git diff stats, etc.
package chips

import (
	"context"
	"os"

	"github.com/Sahaj-Tech-ltd/overkill/internal/prompt"
)

// DirectoryChip reports the current working directory.
type DirectoryChip struct {
	enabled bool
}

// NewDirectoryChip returns a DirectoryChip that starts enabled.
func NewDirectoryChip() *DirectoryChip {
	return &DirectoryChip{enabled: true}
}

// Kind implements prompt.Chip.
func (c *DirectoryChip) Kind() string { return "dir" }

// Title implements prompt.Chip.
func (c *DirectoryChip) Title() string { return "Directory" }

// Value implements prompt.Chip. Returns os.Getwd(). Errors are returned
// as empty string — the chip manager omits them.
func (c *DirectoryChip) Value(_ context.Context) (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return dir, nil
}

// RefreshPolicy implements prompt.Chip.
func (c *DirectoryChip) RefreshPolicy() prompt.RefreshPolicy { return prompt.EveryTurn }

// Enabled implements prompt.Chip.
func (c *DirectoryChip) Enabled() bool { return c.enabled }

// SetEnabled toggles this chip on or off.
func (c *DirectoryChip) SetEnabled(v bool) { c.enabled = v }

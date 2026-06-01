// Package prompt provides a modular context chip system for injecting
// dynamic context into the AI agent's prompt. Inspired by Warp's context
// chips (directory, git branch, git diff stats).
package prompt

import "context"

// RefreshPolicy controls when a chip's value should be regenerated.
type RefreshPolicy int

const (
	// EveryTurn refreshes the chip on every agent turn.
	EveryTurn RefreshPolicy = iota
	// OnChange refreshes only when the value actually changed from the
	// last rendered output.
	OnChange
	// Manual refreshes only when explicitly triggered.
	Manual
)

// Chip represents a single context chip that contributes dynamic
// information to the agent's system prompt.
type Chip interface {
	// Kind returns a unique identifier for this chip (e.g. "dir",
	// "git_branch", "git_diff"). Used for deduplication and UI display.
	Kind() string

	// Title returns the human-readable label for this chip's output
	// (e.g. "Directory", "Git Branch", "Git Diff").
	Title() string

	// Value generates the current value string for this chip. Errors
	// should be handled gracefully — return an empty string and log
	// the error rather than failing. ctx is the agent's per-turn
	// context and should be respected for cancellation.
	Value(ctx context.Context) (string, error)

	// RefreshPolicy returns the chip's refresh strategy.
	RefreshPolicy() RefreshPolicy

	// Enabled returns whether this chip is currently active. Can be
	// toggled per-session.
	Enabled() bool
}

// ChipInfo is the public metadata about a registered chip, suitable for
// UI display (e.g. listing available chips, showing their current state).
type ChipInfo struct {
	Kind          string
	Title         string
	Enabled       bool
	RefreshPolicy RefreshPolicy
}

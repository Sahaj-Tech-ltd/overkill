package chips

import (
	"context"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/prompt"
)

// enforce that each chip type satisfies the prompt.Chip interface at compile time
var _ prompt.Chip = (*DirectoryChip)(nil)
var _ prompt.Chip = (*GitBranchChip)(nil)
var _ prompt.Chip = (*GitDiffChip)(nil)

// --- DirectoryChip ---

func TestDirectoryChipKind(t *testing.T) {
	c := NewDirectoryChip()
	if c.Kind() != "dir" {
		t.Errorf("Kind() = %q, want 'dir'", c.Kind())
	}
}

func TestDirectoryChipTitle(t *testing.T) {
	c := NewDirectoryChip()
	if c.Title() != "Directory" {
		t.Errorf("Title() = %q, want 'Directory'", c.Title())
	}
}

func TestDirectoryChipEnabled(t *testing.T) {
	c := NewDirectoryChip()
	if !c.Enabled() {
		t.Error("new DirectoryChip should be enabled")
	}
}

func TestDirectoryChipSetEnabled(t *testing.T) {
	c := NewDirectoryChip()
	c.SetEnabled(false)
	if c.Enabled() {
		t.Error("after SetEnabled(false), Enabled() should return false")
	}
	c.SetEnabled(true)
	if !c.Enabled() {
		t.Error("after SetEnabled(true), Enabled() should return true")
	}
}

func TestDirectoryChipRefreshPolicy(t *testing.T) {
	c := NewDirectoryChip()
	if c.RefreshPolicy() != prompt.EveryTurn {
		t.Errorf("RefreshPolicy() = %v, want EveryTurn", c.RefreshPolicy())
	}
}

func TestDirectoryChipValue(t *testing.T) {
	c := NewDirectoryChip()
	val, err := c.Value(context.Background())
	if err != nil {
		t.Fatalf("Value() unexpected error: %v", err)
	}
	if val == "" {
		t.Error("Value() returned empty string")
	}
	// The value should be the current working directory — verify it's non-empty
	t.Logf("DirectoryChip.Value() = %q", val)
}

// --- GitBranchChip ---

func TestGitBranchChipKind(t *testing.T) {
	c := NewGitBranchChip()
	if c.Kind() != "git_branch" {
		t.Errorf("Kind() = %q, want 'git_branch'", c.Kind())
	}
}

func TestGitBranchChipTitle(t *testing.T) {
	c := NewGitBranchChip()
	if c.Title() != "Git Branch" {
		t.Errorf("Title() = %q, want 'Git Branch'", c.Title())
	}
}

func TestGitBranchChipEnabled(t *testing.T) {
	c := NewGitBranchChip()
	if !c.Enabled() {
		t.Error("new GitBranchChip should be enabled")
	}
}

func TestGitBranchChipSetEnabled(t *testing.T) {
	c := NewGitBranchChip()
	c.SetEnabled(false)
	if c.Enabled() {
		t.Error("after SetEnabled(false), Enabled() should return false")
	}
}

func TestGitBranchChipRefreshPolicy(t *testing.T) {
	c := NewGitBranchChip()
	if c.RefreshPolicy() != prompt.OnChange {
		t.Errorf("RefreshPolicy() = %v, want OnChange", c.RefreshPolicy())
	}
}

func TestGitBranchChipValueContextCancelled(t *testing.T) {
	c := NewGitBranchChip()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	val, err := c.Value(ctx)
	// Expect an error because the context is already cancelled.
	// This may succeed if git is very fast, but typically the exec will fail.
	if err != nil {
		t.Logf("Expected error from cancelled context: %v", err)
	}
	// Value should still be empty on error
	if val != "" && err == nil {
		t.Logf("Value() returned %q (no git repo or exec succeeded before ctx check)", val)
	}
}

// --- GitDiffChip ---

func TestGitDiffChipKind(t *testing.T) {
	c := NewGitDiffChip()
	if c.Kind() != "git_diff" {
		t.Errorf("Kind() = %q, want 'git_diff'", c.Kind())
	}
}

func TestGitDiffChipTitle(t *testing.T) {
	c := NewGitDiffChip()
	if c.Title() != "Git Diff" {
		t.Errorf("Title() = %q, want 'Git Diff'", c.Title())
	}
}

func TestGitDiffChipEnabled(t *testing.T) {
	c := NewGitDiffChip()
	if !c.Enabled() {
		t.Error("new GitDiffChip should be enabled")
	}
}

func TestGitDiffChipSetEnabled(t *testing.T) {
	c := NewGitDiffChip()
	c.SetEnabled(false)
	if c.Enabled() {
		t.Error("after SetEnabled(false), Enabled() should return false")
	}
}

func TestGitDiffChipRefreshPolicy(t *testing.T) {
	c := NewGitDiffChip()
	if c.RefreshPolicy() != prompt.EveryTurn {
		t.Errorf("RefreshPolicy() = %v, want EveryTurn", c.RefreshPolicy())
	}
}

func TestGitDiffChipValueNoRepoNoError(t *testing.T) {
	c := NewGitDiffChip()
	val, err := c.Value(context.Background())
	// If we're in a git repo with no uncommitted changes, returns "" (no error)
	// If we're not in a git repo, returns "" with an error
	// Either way, Value() should not panic
	if err != nil {
		t.Logf("GitDiffChip.Value() error (expected if no git repo): %v", err)
	}
	if val != "" {
		t.Logf("GitDiffChip.Value() returned diff: %q", val)
	}
}

// --- Interface verification ---

func TestChipsImplementInterface(t *testing.T) {
	// Verify all three chips implement prompt.Chip interface
	chips := []prompt.Chip{
		NewDirectoryChip(),
		NewGitBranchChip(),
		NewGitDiffChip(),
	}

	for _, c := range chips {
		// Ensure Kind() returns a non-empty string
		if c.Kind() == "" {
			t.Errorf("chip has empty Kind()")
		}
		// Ensure Title() returns a non-empty string
		if c.Title() == "" {
			t.Errorf("chip %q has empty Title()", c.Kind())
		}
		// Ensure RefreshPolicy() returns a valid value
		rp := c.RefreshPolicy()
		if rp != prompt.EveryTurn && rp != prompt.OnChange && rp != prompt.Manual {
			t.Errorf("chip %q has invalid RefreshPolicy: %v", c.Kind(), rp)
		}
	}
}

func TestChipConstructionDefaults(t *testing.T) {
	// All constructors create enabled chips
	c1 := NewDirectoryChip()
	c2 := NewGitBranchChip()
	c3 := NewGitDiffChip()

	if !c1.Enabled() || !c2.Enabled() || !c3.Enabled() {
		t.Error("new chips should all be enabled by default")
	}
}

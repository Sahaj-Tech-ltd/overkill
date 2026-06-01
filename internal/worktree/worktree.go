// Package worktree wraps `git worktree` for safe programmatic use.
package worktree

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// Worktree describes one entry from `git worktree list --porcelain`.
type Worktree struct {
	Path     string
	HEAD     string
	Branch   string
	Bare     bool
	Detached bool
	Locked   bool
	Prunable bool
}

// List parses `git worktree list --porcelain` from repoDir.
func List(repoDir string) ([]Worktree, error) {
	out, err := runGit(repoDir, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, fmt.Errorf("worktree: list: %w", err)
	}
	return parsePorcelain(out), nil
}

// parsePorcelain splits the porcelain output into Worktree records.
// Records are separated by a blank line; each line is a key/value pair.
func parsePorcelain(out string) []Worktree {
	var trees []Worktree
	var cur Worktree
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			if cur.Path != "" {
				trees = append(trees, cur)
			}
			cur = Worktree{}
			continue
		}
		switch {
		case strings.HasPrefix(line, "worktree "):
			cur.Path = strings.TrimPrefix(line, "worktree ")
		case strings.HasPrefix(line, "HEAD "):
			cur.HEAD = strings.TrimPrefix(line, "HEAD ")
		case strings.HasPrefix(line, "branch "):
			cur.Branch = strings.TrimPrefix(line, "branch ")
		case line == "bare":
			cur.Bare = true
		case line == "detached":
			cur.Detached = true
		case strings.HasPrefix(line, "locked"):
			cur.Locked = true
		case strings.HasPrefix(line, "prunable"):
			cur.Prunable = true
		}
	}
	if cur.Path != "" {
		trees = append(trees, cur)
	}
	return trees
}

// Add creates a new worktree at `path` checked out to `branch`. If the branch
// doesn't exist, git creates it from HEAD.
func Add(repoDir, path, branch string) error {
	args := []string{"worktree", "add"}
	if branch != "" {
		// -B reuses an existing branch or creates one.
		args = append(args, "-B", branch, path)
	} else {
		args = append(args, path)
	}
	if _, err := runGit(repoDir, args...); err != nil {
		return fmt.Errorf("worktree: add: %w", err)
	}
	return nil
}

// Remove removes a worktree (without --force; caller can opt in via RemoveForce).
func Remove(repoDir, path string) error {
	if _, err := runGit(repoDir, "worktree", "remove", path); err != nil {
		return fmt.Errorf("worktree: remove: %w", err)
	}
	return nil
}

// RemoveForce removes the worktree even if it has uncommitted changes.
func RemoveForce(repoDir, path string) error {
	if _, err := runGit(repoDir, "worktree", "remove", "--force", path); err != nil {
		return fmt.Errorf("worktree: remove --force: %w", err)
	}
	return nil
}

// Lock prevents `git worktree prune` from removing this worktree.
func Lock(repoDir, path, reason string) error {
	args := []string{"worktree", "lock"}
	if reason != "" {
		args = append(args, "--reason", reason)
	}
	args = append(args, path)
	if _, err := runGit(repoDir, args...); err != nil {
		return fmt.Errorf("worktree: lock: %w", err)
	}
	return nil
}

// Unlock reverses Lock.
func Unlock(repoDir, path string) error {
	if _, err := runGit(repoDir, "worktree", "unlock", path); err != nil {
		return fmt.Errorf("worktree: unlock: %w", err)
	}
	return nil
}

func runGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(errBuf.String()))
	}
	return out.String(), nil
}

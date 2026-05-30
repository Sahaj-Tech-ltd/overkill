package agent

import (
	"encoding/json"
	"strings"
)

// CheckpointSnapshotter is the tiny interface the agent uses to take a
// filesystem snapshot before a destructive tool call (master plan §4.8:
// "Filesystem checkpoints before destructive operations" — should be
// automatic, not at the LLM's discretion).
//
// internal/checkpoint.Manager satisfies this via a thin cmd/overkill
// adapter so the agent doesn't import internal/checkpoint directly.
type CheckpointSnapshotter interface {
	// Snapshot captures the listed paths under sessionID with a one-line
	// reason. Returning an error must NOT block the tool call — the
	// caller logs and proceeds. A missing snapshotter is a no-op.
	Snapshot(sessionID, reason string, paths []string) error
}

// SetCheckpointSnapshotter installs the auto-snapshot hook. Pass nil to
// disable.
func (a *Agent) SetCheckpointSnapshotter(s CheckpointSnapshotter) {
	a.mu.Lock()
	a.checkpointSnapshotter = s
	a.mu.Unlock()
}

// SetCheckpointManager wires the git-based checkpoint manager for
// user-facing /snapshot, /rollback, and /snapshots slash commands.
// Pass nil to disable.
func (a *Agent) SetCheckpointManager(cm *CheckpointManager) {
	a.mu.Lock()
	a.checkpointManager = cm
	a.mu.Unlock()
}

// preToolCheckpoint snapshots affected paths before a destructive tool
// fires. No-op when no snapshotter is wired, when the tool isn't
// destructive, or when no affected paths can be inferred. The error is
// returned so callers can decide whether to emit an alert; the tool
// call should NOT be blocked on snapshot failure — the user's intent is
// always primary, the snapshot is a safety net.
func (a *Agent) preToolCheckpoint(toolName, args string) (string, error) {
	a.mu.RLock()
	snap := a.checkpointSnapshotter
	cm := a.checkpointManager
	sid := a.sessionID
	a.mu.RUnlock()

	// Auto-snapshot via the git-based checkpoint manager when the tool
	// is risky. This is fire-and-forget — errors are returned so the
	// caller can log them, but the tool call proceeds regardless.
	if cm != nil && IsRisky(toolName) {
		// Synchronous snapshot: the checkpoint must capture state BEFORE
		// the tool modifies files. The prior async goroutine raced with
		// tool execution and could snapshot after changes landed.
		_, _ = cm.Snapshot("auto-before-" + toolName)
	}

	if snap == nil {
		return "", nil
	}
	paths := destructivePaths(toolName, args)
	// nil paths = not destructive / can't infer → skip.
	// Empty slice = "snapshot the whole tracked workspace" — used for
	// destructive shell commands where we can't enumerate paths.
	if paths == nil {
		return "", nil
	}
	reason := "pre-" + toolName + " auto-snapshot"
	if err := snap.Snapshot(sid, reason, paths); err != nil {
		return reason, err
	}
	return reason, nil
}

// destructivePaths returns the set of filesystem paths a destructive
// tool will touch, or nil when the tool is not destructive / has no
// paths to snapshot. We deliberately keep this conservative — the cost
// of snapshotting an extra path is negligible; missing one is a
// missed rollback.
func destructivePaths(toolName, args string) []string {
	switch toolName {
	case "patch", "fs_write", "write_file", "fs_edit", "fs_delete":
		var v struct {
			Path string `json:"path"`
			File string `json:"file"`
		}
		if err := json.Unmarshal([]byte(args), &v); err != nil {
			return nil
		}
		switch {
		case v.Path != "":
			return []string{v.Path}
		case v.File != "":
			return []string{v.File}
		}
		return nil
	case "shell", "pty_shell", "execute_command":
		// Shell commands are opaque — we can't know the exact paths
		// they'll touch. Snapshot the whole working tree by passing an
		// empty paths slice with a sentinel reason; the snapshotter is
		// expected to treat empty paths as "everything tracked".
		var v struct {
			Command string `json:"command"`
			Cmd     string `json:"cmd"`
		}
		if err := json.Unmarshal([]byte(args), &v); err != nil {
			return nil
		}
		cmd := v.Command
		if cmd == "" {
			cmd = v.Cmd
		}
		if !looksDestructiveShell(cmd) {
			return nil
		}
		// Empty slice → snapshotter snapshots the whole workspace.
		return []string{}
	}
	return nil
}

// looksDestructiveShell flags shell commands that modify or delete files.
// Conservative: false positives are fine (we just snapshot more often
// than strictly needed); false negatives miss a rollback.
func looksDestructiveShell(cmd string) bool {
	low := strings.ToLower(cmd)
	for _, needle := range []string{
		"rm ", "rm\t", " rm\n", "rmdir ", "mv ", "cp -f", "cp --force",
		">", ">>", "tee ", "sed -i", "perl -i", "truncate ",
		"git reset --hard", "git clean", "git checkout --",
		"chmod ", "chown ", "mkfs", "dd if=", "shred ",
	} {
		if strings.Contains(low, needle) {
			return true
		}
	}
	return false
}

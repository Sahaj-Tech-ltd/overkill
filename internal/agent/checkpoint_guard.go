package agent

import (
	"encoding/json"
	"fmt"
	"github.com/Sahaj-Tech-ltd/overkill/internal/security"
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
	// reason. Returning an error blocks the tool call when recovery is enabled.
	// A missing snapshotter is a no-op for embedding/test callers.
	Snapshot(sessionID, reason string, paths []string) error
}

// CheckpointReceipt exposes the actual recovery scope to user-facing clients.
type CheckpointReceipt struct {
	ID      string `json:"id"`
	Scope   string `json:"scope"`
	Entries int    `json:"entries"`
	Notice  string `json:"notice"`
}
type checkpointReceipter interface {
	SnapshotReceipt(sessionID, reason string, paths []string) (CheckpointReceipt, error)
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
// call must be blocked on snapshot failure when recovery is enabled.
func (a *Agent) preToolCheckpoint(toolName, args string) (string, error) {
	if a.PrivilegeMode() == security.ModeReader {
		return "", nil
	}
	a.mu.RLock()
	snap := a.checkpointSnapshotter
	cm := a.checkpointManager
	a.mu.RUnlock()
	sid := a.SessionID()

	// Auto-snapshot via the git-based checkpoint manager when the tool
	// is risky. This legacy manager is explicitly installed by callers only;
	// shared production assembly uses the filesystem snapshotter instead.
	if cm != nil && IsRisky(toolName) {
		// Synchronous snapshot: the checkpoint must capture state BEFORE
		// the tool modifies files. The prior async goroutine raced with
		// tool execution and could snapshot after changes landed.
		if _, err := cm.Snapshot("auto-before-" + toolName); err != nil {
			return "auto-before-" + toolName, err
		}
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
	if receiver, ok := snap.(checkpointReceipter); ok {
		receipt, err := receiver.SnapshotReceipt(sid, reason, paths)
		if err != nil {
			return reason, err
		}
		a.emit("checkpoint_created", map[string]any{"checkpoint": receipt, "session_id": sid, "tool": toolName})
		return fmt.Sprintf("Checkpoint %s saved (%s; %d files). %s", receipt.ID, receipt.Scope, receipt.Entries, receipt.Notice), nil
	}
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
	case "fs", "patch", "fs_patch", "apply_patch", "fs_write", "write_file", "fs_edit", "edit_file", "fs_delete":
		var v struct {
			Action   string   `json:"action"`
			Path     string   `json:"path"`
			File     string   `json:"file"`
			FilePath string   `json:"file_path"`
			Paths    []string `json:"paths"`
			Files    []struct {
				Path     string `json:"path"`
				File     string `json:"file"`
				FilePath string `json:"file_path"`
			} `json:"files"`
		}
		// Invalid input is rejected by the actual tool parser, before any mutation.
		if json.Unmarshal([]byte(args), &v) != nil {
			return nil
		}
		if toolName == "fs" {
			switch v.Action {
			case "read", "glob", "stat", "mkdir":
				return nil
			}
		}
		paths := append([]string(nil), v.Paths...)
		for _, path := range []string{v.Path, v.File, v.FilePath} {
			if path != "" {
				paths = append(paths, path)
			}
		}
		for _, file := range v.Files {
			for _, path := range []string{file.Path, file.File, file.FilePath} {
				if path != "" {
					paths = append(paths, path)
				}
			}
		}
		if len(paths) == 0 {
			return []string{}
		} // opaque mutation: capture the workspace
		return paths
	case "shell", "bash", "pty_shell", "execute_command":
		var v struct {
			Command string `json:"command"`
			Cmd     string `json:"cmd"`
		}
		if json.Unmarshal([]byte(args), &v) != nil {
			return nil
		}
		command := v.Command
		if command == "" {
			command = v.Cmd
		}
		if looksDestructiveShell(command) {
			return []string{}
		}
	case "git":
		// Git can change the working tree; automatic checkpoints never stage or
		// commit the user's index. Git metadata itself is outside the file scope.
		return []string{}
	}
	return nil
}

// Arbitrary shell programs, including apparent observations, may write through
// options, hooks, substitutions or wrappers. Initially every shell call requires
// a checkpoint; this deliberately avoids a second incomplete shell allowlist.
func looksDestructiveShell(command string) bool { return command != "" }

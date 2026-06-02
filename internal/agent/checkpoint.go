package agent

import (
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Snapshot represents one git-based filesystem checkpoint.
type Snapshot struct {
	Hash    string
	Message string
	Time    time.Time
}

// CheckpointManager uses git to create, list, and roll back filesystem
// snapshots in a working directory. It's wired into the agent for the
// /snapshot and /rollback slash commands, and optionally for automatic
// pre-tool checkpoints.
type CheckpointManager struct {
	mu      sync.Mutex
	workDir string
}

// NewCheckpointManager returns a CheckpointManager rooted at workDir.
// No git operations are performed until Snapshot/Rollback/ListSnapshots
// is called.
func NewCheckpointManager(workDir string) *CheckpointManager {
	return &CheckpointManager{workDir: workDir}
}

// WorkDir returns the working directory this manager operates on.
func (cm *CheckpointManager) WorkDir() string { return cm.workDir }

// Snapshot stages all changes (git add -A) and commits them with the
// message "checkpoint: <name>". Returns the new commit hash.
func (cm *CheckpointManager) Snapshot(name string) (string, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if err := cm.verifyRepo(); err != nil {
		return "", err
	}

	// Stage everything.
	add := exec.Command("git", "add", "-A")
	add.Dir = cm.workDir
	if out, err := add.CombinedOutput(); err != nil {
		return "", fmt.Errorf("git add -A: %s: %w", strings.TrimSpace(string(out)), err)
	}

	// Commit.
	msg := "checkpoint: " + name
	commit := exec.Command("git", "commit", "--allow-empty", "-m", msg)
	commit.Dir = cm.workDir
	if out, err := commit.CombinedOutput(); err != nil {
		return "", fmt.Errorf("git commit: %s: %w", strings.TrimSpace(string(out)), err)
	}

	// Return the new HEAD hash.
	hash := exec.Command("git", "rev-parse", "HEAD")
	hash.Dir = cm.workDir
	hashOut, err := hash.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git rev-parse: %s: %w", strings.TrimSpace(string(hashOut)), err)
	}
	return strings.TrimSpace(string(hashOut)), nil
}

// Rollback resets the working tree to the Nth most recent checkpoint
// (0 = latest) using `git reset --hard HEAD~<n>`. Returns a
// human-readable summary of the new HEAD.
func (cm *CheckpointManager) Rollback(n int) (string, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if err := cm.verifyRepo(); err != nil {
		return "", err
	}

	cmd := exec.Command("git", "reset", "--hard", fmt.Sprintf("HEAD~%d", n))
	cmd.Dir = cm.workDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git reset: %s: %w", strings.TrimSpace(string(out)), err)
	}

	// Read the new HEAD.
	hash := exec.Command("git", "rev-parse", "HEAD")
	hash.Dir = cm.workDir
	hashOut, err := hash.CombinedOutput()
	if err != nil {
		return strings.TrimSpace(string(hashOut)),
			fmt.Errorf("rollback succeeded but rev-parse failed: %w", err)
	}
	newHead := strings.TrimSpace(string(hashOut))

	if n == 0 {
		return fmt.Sprintf("rolled back 1 checkpoint. now at %s", newHead), nil
	}
	return fmt.Sprintf("rolled back %d checkpoint(s). now at %s", n, newHead), nil
}

// ListSnapshots returns all checkpoint commits in reverse chronological
// order (newest first). Only commits with "checkpoint:" in the message
// are included.
func (cm *CheckpointManager) ListSnapshots() ([]Snapshot, error) {
	if err := cm.verifyRepo(); err != nil {
		return nil, err
	}

	// --format produces: <full hash> <subject> <ISO 8601 date>
	cmd := exec.Command("git", "log", "--oneline",
		"--grep=checkpoint:",
		"--format=%H %s %aI")
	cmd.Dir = cm.workDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		// No commits matching the grep is exit 128; treat as empty.
		exitErr, ok := err.(*exec.ExitError)
		if ok && exitErr.ExitCode() == 128 {
			return nil, nil
		}
		return nil, fmt.Errorf("git log: %s: %w", strings.TrimSpace(string(out)), err)
	}

	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return nil, nil
	}

	var snaps []Snapshot
	// Line format: <full-hash> checkpoint: <name> <ISO-8601>
	re := regexp.MustCompile(`^([0-9a-f]{40}) (checkpoint: .+) (\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}.*)$`)
	for _, line := range strings.Split(trimmed, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		m := re.FindStringSubmatch(line)
		if len(m) != 4 {
			continue
		}
		t, err := time.Parse(time.RFC3339, m[3])
		if err != nil {
			t = time.Time{}
		}
		snaps = append(snaps, Snapshot{
			Hash:    m[1],
			Message: m[2],
			Time:    t,
		})
	}
	return snaps, nil
}

// FormatSnapshots returns a human-readable listing suitable for display
// in chat. Returns an empty string with no error when there are no
// checkpoints.
func (cm *CheckpointManager) FormatSnapshots() (string, error) {
	snaps, err := cm.ListSnapshots()
	if err != nil {
		return "", err
	}
	if len(snaps) == 0 {
		return "no checkpoints yet — use /snapshot [name] to create one.", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d checkpoint(s):\n", len(snaps))
	for i, s := range snaps {
		short := s.Hash
		if len(short) > 7 {
			short = short[:7]
		}
		ts := ""
		if !s.Time.IsZero() {
			ts = s.Time.Format("2006-01-02 15:04")
		}
		fmt.Fprintf(&b, "%d. `%s`  %s  %s\n", i+1, short, s.Message, ts)
	}
	b.WriteString("\n/rollback 0 rolls back to the latest. /rollback N goes N checkpoints back.")
	return b.String(), nil
}

// verifyRepo checks that workDir is inside a git repository.
func (cm *CheckpointManager) verifyRepo() error {
	cmd := exec.Command("git", "rev-parse", "--git-dir")
	cmd.Dir = cm.workDir
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("not a git repository (or git not installed): %s: %w", cm.workDir, err)
	}
	return nil
}

// riskyToolNames is the set of tool names the checkpoint manager will
// auto-snapshot before. Conservative: false positives cost a commit;
// false negatives lose a rollback target.
var riskyToolNames = map[string]bool{
	"shell":           true,
	"terminal":        true,
	"write_file":      true,
	"patch":           true,
	"fs_write":        true,
	"fs_edit":         true,
	"fs_delete":       true,
	"pty_shell":       true,
	"execute_command": true,
}

// IsRisky returns true when toolName is a destructive tool that should
// trigger an auto-snapshot before execution.
func IsRisky(toolName string) bool {
	return riskyToolNames[toolName]
}

package agent

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// initTestRepo creates a temp directory with an initialized git repo.
// Returns the path to the repo directory and a cleanup function.
func initTestRepo(t *testing.T) (string, func()) {
	t.Helper()
	dir, err := os.MkdirTemp("", "overkill-checkpoint-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}

	// git init
	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		os.RemoveAll(dir)
		t.Fatalf("git init: %s: %v", string(out), err)
	}

	// Configure git user so commits work in CI/test environments.
	for _, cfg := range [][2]string{
		{"user.email", "test@overkill.local"},
		{"user.name", "Overkill Test"},
	} {
		c := exec.Command("git", "config", cfg[0], cfg[1])
		c.Dir = dir
		if out, err := c.CombinedOutput(); err != nil {
			os.RemoveAll(dir)
			t.Fatalf("git config %s: %s: %v", cfg[0], string(out), err)
		}
	}

	// Initial commit so HEAD is not unborn.
	if err := os.WriteFile(filepath.Join(dir, ".gitkeep"), []byte("init"), 0644); err != nil {
		os.RemoveAll(dir)
		t.Fatalf("write .gitkeep: %v", err)
	}
	add := exec.Command("git", "add", ".gitkeep")
	add.Dir = dir
	if out, err := add.CombinedOutput(); err != nil {
		os.RemoveAll(dir)
		t.Fatalf("git add: %s: %v", string(out), err)
	}
	commit := exec.Command("git", "commit", "-m", "initial commit")
	commit.Dir = dir
	if out, err := commit.CombinedOutput(); err != nil {
		os.RemoveAll(dir)
		t.Fatalf("git commit: %s: %v", string(out), err)
	}

	return dir, func() { os.RemoveAll(dir) }
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
		t.Fatalf("writeFile %s: %v", name, err)
	}
}

// commitCount returns the number of commits in the repo.
func commitCount(t *testing.T, dir string) int {
	t.Helper()
	cmd := exec.Command("git", "rev-list", "--count", "HEAD")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git rev-list: %s: %v", string(out), err)
	}
	var n int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &n); err != nil {
		t.Fatalf("parse count: %v", err)
	}
	return n
}

func TestCheckpoint_Snapshot(t *testing.T) {
	dir, cleanup := initTestRepo(t)
	defer cleanup()

	cm := NewCheckpointManager(dir)

	// Write a file and snapshot.
	writeFile(t, dir, "hello.txt", "hello world")
	hash, err := cm.Snapshot("test-snap-1")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(hash) != 40 {
		t.Errorf("expected 40-char hash, got %q (%d chars)", hash, len(hash))
	}

	// Verify the commit exists with the right message.
	cmd := exec.Command("git", "log", "-1", "--format=%s", hash)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git log: %s: %v", string(out), err)
	}
	msg := strings.TrimSpace(string(out))
	if msg != "checkpoint: test-snap-1" {
		t.Errorf("commit message = %q, want %q", msg, "checkpoint: test-snap-1")
	}

	// Verify the file is tracked and committed.
	cmd2 := exec.Command("git", "show", hash+":hello.txt")
	cmd2.Dir = dir
	out2, err := cmd2.CombinedOutput()
	if err != nil {
		t.Fatalf("git show: %s: %v", string(out2), err)
	}
	if strings.TrimSpace(string(out2)) != "hello world" {
		t.Errorf("file content = %q, want %q", string(out2), "hello world")
	}
}

func TestCheckpoint_Rollback(t *testing.T) {
	dir, cleanup := initTestRepo(t)
	defer cleanup()

	cm := NewCheckpointManager(dir)

	// Create first snapshot with file A.
	writeFile(t, dir, "file.txt", "version 1")
	hash1, err := cm.Snapshot("first")
	if err != nil {
		t.Fatalf("Snapshot first: %v", err)
	}

	// Create second snapshot with file A modified.
	writeFile(t, dir, "file.txt", "version 2")
	_, err = cm.Snapshot("second")
	if err != nil {
		t.Fatalf("Snapshot second: %v", err)
	}

	// Verify current content is version 2.
	content, _ := os.ReadFile(filepath.Join(dir, "file.txt"))
	if string(content) != "version 2" {
		t.Fatalf("pre-rollback content = %q, want %q", content, "version 2")
	}

	// Rollback 0 (to latest checkpoint = "second" — stays version 2).
	_, err = cm.Rollback(0)
	if err != nil {
		t.Fatalf("Rollback(0): %v", err)
	}
	content, _ = os.ReadFile(filepath.Join(dir, "file.txt"))
	if string(content) != "version 2" {
		t.Errorf("after rollback(0) content = %q, want %q", content, "version 2")
	}

	// Rollback 1 (to "first" checkpoint = version 1).
	_, err = cm.Rollback(1)
	if err != nil {
		t.Fatalf("Rollback(1): %v", err)
	}
	content, _ = os.ReadFile(filepath.Join(dir, "file.txt"))
	if string(content) != "version 1" {
		t.Errorf("after rollback(1) content = %q, want %q", content, "version 1")
	}

	_ = hash1 // used
}

func TestCheckpoint_ListSnapshots(t *testing.T) {
	dir, cleanup := initTestRepo(t)
	defer cleanup()

	cm := NewCheckpointManager(dir)

	// No snapshots yet (only the initial commit, which doesn't match "checkpoint:").
	snaps, err := cm.ListSnapshots()
	if err != nil {
		t.Fatalf("ListSnapshots empty: %v", err)
	}
	if len(snaps) != 0 {
		t.Fatalf("expected 0 snapshots, got %d", len(snaps))
	}

	// Create several checkpoints.
	for _, name := range []string{"A", "B", "C"} {
		writeFile(t, dir, "marker.txt", name)
		if _, err := cm.Snapshot(name); err != nil {
			t.Fatalf("Snapshot %s: %v", name, err)
		}
	}

	snaps, err = cm.ListSnapshots()
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	if len(snaps) != 3 {
		t.Fatalf("expected 3 snapshots, got %d", len(snaps))
	}

	// Newest first.
	if !strings.Contains(snaps[0].Message, "C") {
		t.Errorf("first snapshot should be C, got %q", snaps[0].Message)
	}
	if !strings.Contains(snaps[2].Message, "A") {
		t.Errorf("last snapshot should be A, got %q", snaps[2].Message)
	}
}

func TestCheckpoint_FormatSnapshots(t *testing.T) {
	dir, cleanup := initTestRepo(t)
	defer cleanup()

	cm := NewCheckpointManager(dir)

	// Empty repo (only initial commit — no checkpoints).
	out, err := cm.FormatSnapshots()
	if err != nil {
		t.Fatalf("FormatSnapshots empty: %v", err)
	}
	if !strings.Contains(out, "no checkpoints") {
		t.Errorf("expected 'no checkpoints' message, got: %q", out)
	}

	// Add a checkpoint.
	writeFile(t, dir, "x.txt", "x")
	_, err = cm.Snapshot("my-snap")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	out, err = cm.FormatSnapshots()
	if err != nil {
		t.Fatalf("FormatSnapshots: %v", err)
	}
	if !strings.Contains(out, "checkpoint: my-snap") {
		t.Errorf("expected snapshot listing, got: %q", out)
	}
	if !strings.Contains(out, "1 checkpoint(s)") {
		t.Errorf("expected count, got: %q", out)
	}
}

func TestCheckpoint_NonGitDir(t *testing.T) {
	dir, err := os.MkdirTemp("", "overkill-nongit-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(dir)

	cm := NewCheckpointManager(dir)

	_, err = cm.Snapshot("test")
	if err == nil || !strings.Contains(err.Error(), "not a git repository") {
		t.Errorf("expected 'not a git repository' error, got: %v", err)
	}

	_, err = cm.Rollback(0)
	if err == nil || !strings.Contains(err.Error(), "not a git repository") {
		t.Errorf("expected 'not a git repository' error, got: %v", err)
	}

	_, err = cm.ListSnapshots()
	if err == nil || !strings.Contains(err.Error(), "not a git repository") {
		t.Errorf("expected 'not a git repository' error, got: %v", err)
	}
}

func TestCheckpoint_IsRisky(t *testing.T) {
	risky := []string{"shell", "terminal", "write_file", "patch", "fs_write", "fs_edit", "fs_delete"}
	for _, name := range risky {
		if !IsRisky(name) {
			t.Errorf("%q should be risky", name)
		}
	}

	safe := []string{"fs_read", "read_file", "grep", "search", "list", "cat"}
	for _, name := range safe {
		if IsRisky(name) {
			t.Errorf("%q should NOT be risky", name)
		}
	}
}

package security

import (
	"os"
	"path/filepath"
	"testing"
)

// TestNullByteBypass verifies paths containing \x00 are rejected.
// BUG #166: filepath.Clean preserves \x00, kernel truncates → traversal.
func TestNullByteBypass(t *testing.T) {
	base := t.TempDir()

	// Real attack: null byte in the user-supplied name component.
	// filepath.Clean("../../../etc/passwd\x00.txt") preserves the \x00.
	// filepath.Join(base, cleaned) produces e.g. "/tmp/base/etc/passwd\x00.txt".
	// The prefix check PASSES because the joined path starts with base.
	// But the kernel truncates at \x00 → actual file opened is /etc/passwd.
	_, err := SafePath(base, "../../../etc/passwd\x00.txt")
	if err == nil {
		t.Error("BUG #166: SafePath accepted null-byte in name — kernel would truncate to /etc/passwd")
	}

	// Null byte in dir should also be rejected.
	_, err = SafePath(base+"\x00extra", "file.txt")
	if err == nil {
		t.Error("BUG #166: SafePath accepted null-byte in dir")
	}

	// Empty null byte should be rejected.
	_, err = SafePath(base, "\x00")
	if err == nil {
		t.Error("BUG #166: SafePath accepted lone null byte in name")
	}
}

// TestSymlinkErrorNotSilent verifies symlink check failure is not silently ignored.
// BUG #167: If EvalSymlinks fails, containment is skipped.
func TestSymlinkErrorNotSilent(t *testing.T) {
	base := t.TempDir()

	// Sanity: normal safe path inside base should work.
	_, err := SafePath(base, "file.txt")
	if err != nil {
		t.Fatalf("unexpected error for valid path: %v", err)
	}

	// Traversal via ".." should be caught.
	_, err = SafePath(base, "../../../etc/passwd")
	if err == nil {
		t.Error("BUG #167: SafePath accepted traversal via .. outside base")
	}

	// Traversal via symlink inside base should be caught.
	// Create a symlink inside base that points outside.
	symlinkPath := filepath.Join(base, "escape")
	outsideTarget := filepath.Join(base, "..", "outside_target")
	os.MkdirAll(outsideTarget, 0o755)
	if err := os.Symlink(outsideTarget, symlinkPath); err != nil {
		t.Skipf("cannot create symlink (Windows or permission): %v", err)
	}

	_, err = SafePath(base, "escape/secret.txt")
	if err == nil {
		t.Error("BUG #167: SafePath accepted path through symlink that escapes base")
	}
}

// Package atomicfile provides crash-safe file writes via the
// temp-then-rename pattern. A bare os.WriteFile can leave a truncated
// or zero-byte file on the disk if the process crashes (or the host
// loses power) mid-write — the next boot then loads the corrupted file
// and silently drops whatever was in it. Across the project this hit
// real persisted state: alerts.json dropped pending alerts, the
// flight recorder dropped its tail, personality/soul.md left an
// empty file that re-triggered the cold-start flow.
//
// WriteFile is the drop-in replacement: same signature as os.WriteFile,
// same semantics on success, but the on-disk file is either the OLD
// contents or the FULL new contents — never a half-written truncation.
package atomicfile

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// WriteFile writes data to path atomically. The temp file is created
// in the same directory as path so the final rename stays on the same
// filesystem (rename across filesystems is not atomic on Linux).
//
// Permissions are applied to the temp file before rename, so the
// final file inherits perm and never has a wider transient mode.
// The temp file is fsync'd before rename so a crash after rename
// cannot expose an empty file caused by buffer reordering.
//
// On any error after temp file creation, the temp is removed
// best-effort. Callers see a single error; the on-disk state
// reflects the prior file (or absence) exactly.
func WriteFile(path string, data []byte, perm fs.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp.*")
	if err != nil {
		return fmt.Errorf("atomicfile: create temp: %w", err)
	}
	tmpPath := tmp.Name()
	// Best-effort cleanup on any error path. Successful rename
	// removes the temp implicitly so this becomes a no-op.
	defer func() {
		_ = os.Remove(tmpPath)
	}()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("atomicfile: write temp: %w", err)
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return fmt.Errorf("atomicfile: chmod temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("atomicfile: sync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("atomicfile: close temp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("atomicfile: rename: %w", err)
	}
	return nil
}

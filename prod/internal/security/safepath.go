// Package security provides shared security primitives used across
// internal packages — path containment, credential masking, and
// prompt-injection detection.
package security

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SafePath joins a trusted directory with a caller-supplied name,
// enforcing that the resulting path stays within the directory.
//
// It rejects:
//   - Absolute paths (e.g. "/etc/passwd").
//   - Path traversal via ".." components.
//   - Symlink-based escapes (by resolving absolute paths and checking
//     the prefix).
//
// Returns the sanitised full path on success.
func SafePath(dir, name string) (string, error) {
	// BUG #166: Reject null bytes before any path operations.
	// filepath.Clean preserves \x00, but the kernel truncates at \x00,
	// which would allow path traversal after kernel truncation.
	if strings.ContainsRune(dir, 0) || strings.ContainsRune(name, 0) {
		return "", fmt.Errorf("security: path contains null byte")
	}

	cleaned := filepath.Clean(name)
	if filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("security: absolute paths not allowed")
	}
	full := filepath.Join(dir, cleaned)

	// Resolve both to absolute so symlink tricks can't escape.
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("security: cannot resolve directory: %w", err)
	}
	absFull, err := filepath.Abs(full)
	if err != nil {
		return "", fmt.Errorf("security: cannot resolve path: %w", err)
	}

	// Must be exactly dir OR a descendant of dir+separator.
	if !strings.HasPrefix(absFull, absDir+string(os.PathSeparator)) && absFull != absDir {
		return "", fmt.Errorf("security: path traversal blocked")
	}

	// Resolve symlinks on the directory (if it exists).
	// A symlinked root directory could bypass the prefix check above
	// if filepath.Abs does not follow the symlink.
	resolvedDir, err := filepath.EvalSymlinks(absDir)
	if err != nil {
		if !os.IsNotExist(err) {
			// BUG #167: Don't silently skip the containment check when
			// EvalSymlinks fails for a reason other than "not exists".
			// A permission error, I/O error, or other failure means we
			// cannot verify the directory is not a symlink escape.
			return "", fmt.Errorf("security: cannot resolve directory symlinks: %w", err)
		}
		// Directory doesn't exist yet — the lexical prefix check above
		// already provides containment.  For not-yet-created files this
		// is acceptable.
	} else {
		// Resolve symlinks for the full path.  If the file doesn't exist
		// yet, walk up the ancestor chain to the deepest existing parent
		// and check *its* resolved path — a symlink in a parent directory
		// can still provide an escape even for not-yet-created files.
		checkPath := absFull
		for {
			resolvedFull, err := filepath.EvalSymlinks(checkPath)
			if err == nil {
				if !strings.HasPrefix(resolvedFull, resolvedDir+string(os.PathSeparator)) && resolvedFull != resolvedDir {
					return "", fmt.Errorf("security: path traversal blocked")
				}
				break
			}
			if !os.IsNotExist(err) {
				// Symlink cycle, permission error, or other resolution
				// error on an existing path — cannot verify containment.
				return "", fmt.Errorf("security: cannot resolve path: %w", err)
			}
			// Walk up one level and try again.
			parent := filepath.Dir(checkPath)
			if parent == checkPath || parent == resolvedDir {
				// Reached filesystem root or the base directory itself —
				// the lexical prefix check already covers this.
				break
			}
			checkPath = parent
		}
	}

	return full, nil
}

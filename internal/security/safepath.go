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

	return full, nil
}

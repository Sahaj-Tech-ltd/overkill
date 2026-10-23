package tools

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// openWorkspace anchors every subsequent operation to a directory descriptor.
// Unlike a path check followed by os.Open, os.Root enforces containment during
// traversal, including when another process swaps a parent for a symlink.
func openWorkspace(dir, name string) (*os.Root, string, error) {
	if name == "" {
		return nil, "", fmt.Errorf("path is required")
	}
	if strings.ContainsRune(name, 0) {
		return nil, "", fmt.Errorf("path contains null byte")
	}
	if filepath.IsAbs(name) {
		root, err := filepath.Abs(dir)
		if err != nil {
			return nil, "", err
		}
		rel, err := filepath.Rel(root, name)
		if err != nil {
			return nil, "", err
		}
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			// Accept an absolute path spelled with the root's canonical name
			// (for example /private/tmp versus /tmp on macOS).
			canonicalRoot, rootErr := filepath.EvalSymlinks(root)
			canonicalName, nameErr := canonicalExistingParent(name)
			if rootErr != nil {
				return nil, "", rootErr
			}
			if nameErr != nil {
				return nil, "", nameErr
			}
			rel, err = filepath.Rel(canonicalRoot, canonicalName)
			if err != nil {
				return nil, "", err
			}
		}
		name = rel
	}
	name = filepath.Clean(name)
	if name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
		return nil, "", fmt.Errorf("path traversal rejected")
	}
	root, err := os.OpenRoot(dir)
	return root, name, err
}

// atomicWorkspaceWrite pins the parent before creating or renaming anything.
// Final symlinks are refused for mutations: replacing the link would silently
// change the meaning of an edit, while following it after validation races.
func atomicWorkspaceWrite(root *os.Root, name string, data []byte) error {
	parent, err := root.OpenRoot(filepath.Dir(name))
	if err != nil {
		return err
	}
	defer parent.Close()
	base := filepath.Base(name)
	mode := os.FileMode(0o600)
	if info, err := parent.Lstat(base); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("refusing to replace non-regular file %s", name)
		}
		mode = info.Mode().Perm()
	} else if !os.IsNotExist(err) {
		return err
	}
	temp := fmt.Sprintf(".overkill-%x.tmp", rand.Text())
	fh, err := parent.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer parent.Remove(temp)
	if _, err = fh.Write(data); err == nil {
		err = fh.Chmod(mode)
	}
	if err == nil {
		err = fh.Sync()
	}
	closeErr := fh.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return parent.Rename(temp, base)
}

// canonicalExistingParent handles a new file beneath an existing aliased root.
// Returned names are still opened through os.Root; this is normalization only.
func canonicalExistingParent(name string) (string, error) {
	current := name
	var suffix []string
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			return resolved, nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", err
		}
		suffix = append(suffix, filepath.Base(current))
		current = parent
	}
}

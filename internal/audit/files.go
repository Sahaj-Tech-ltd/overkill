package audit

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Respect git's tracked/unignored scope, so downloaded research sources,
// dependency caches, and build artifacts are not audited as project modules.
func projectFiles(ctx context.Context, root string) ([]string, error) {
	commandCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(commandCtx, "git", "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	cmd.Dir = root
	output, err := cmd.Output()
	if err == nil {
		if len(output) > 4*1024*1024 {
			return nil, fmt.Errorf("audit: project file inventory exceeds 4 MiB")
		}
		files := []string{}
		for _, raw := range bytes.Split(output, []byte{0}) {
			if len(raw) > 0 {
				files = append(files, filepath.Join(root, string(raw)))
			}
		}
		return files, nil
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	files := []string{}
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path != root && skipDirectory(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink == 0 {
			files = append(files, path)
		}
		if len(files) > 50000 {
			return fmt.Errorf("audit: project file inventory exceeds 50000 files")
		}
		return ctx.Err()
	})
	return files, err
}

func containedClaim(root, path string) (string, error) {
	if filepath.IsAbs(path) {
		return "", fmt.Errorf("audit: claim path must be relative")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	target, err := filepath.EvalSymlinks(filepath.Join(root, path))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("audit: claim path escapes workspace")
	}
	return target, nil
}

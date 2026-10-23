package automemory

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Only OS aliases above the trusted config/home root may resolve symlinks.
// Every memory-owned component below it must be an ordinary directory.
func (e *Extractor) safeDirectory(create bool) (string, error) {
	base := e.HomeDir
	if e.configHome != "" {
		base = e.configHome
	}
	if base == "" {
		base = "."
	}
	absolute, err := filepath.Abs(base)
	if err != nil {
		return "", err
	}
	root, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	target, err := filepath.Abs(e.dir())
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(absolute, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("automemory: path outside state root")
	}
	current := root
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) && create {
			err = os.Mkdir(current, 0700)
			if err != nil && !os.IsExist(err) {
				return "", err
			}
			info, err = os.Lstat(current)
		}
		if err != nil {
			return "", err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("automemory: unsafe store directory")
		}
	}
	return current, nil
}

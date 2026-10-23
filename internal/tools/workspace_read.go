package tools

import (
	"context"
	"fmt"
	"io"
)

// ReadWorkspaceFile reads a bounded prefix through a pinned workspace root.
// It follows contained symlinks, refuses escapes/non-regular files, and reports
// truncation without first allocating or reading the entire input file.
func ReadWorkspaceFile(ctx context.Context, dir, name string, limit int) ([]byte, bool, error) {
	if dir == "" || limit < 1 || limit > 20<<20 {
		return nil, false, fmt.Errorf("workspace root and bounded read limit required")
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	root, relative, err := openWorkspace(dir, name)
	if err != nil {
		return nil, false, err
	}
	defer root.Close()
	file, err := openVisionFile(root, relative)
	if err != nil {
		return nil, false, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, false, err
	}
	if !info.Mode().IsRegular() {
		return nil, false, fmt.Errorf("not a regular workspace file")
	}
	data, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil {
		return nil, false, err
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if len(data) > limit {
		return data[:limit], true, nil
	}
	return data, false, nil
}

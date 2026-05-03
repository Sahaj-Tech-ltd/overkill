package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type FSTool struct {
	rootDir string
}

type FSInput struct {
	Action  string `json:"action"`
	Path    string `json:"path"`
	Content string `json:"content"`
	Old     string `json:"old"`
	New     string `json:"new"`
	Pattern string `json:"pattern"`
	Offset  int    `json:"offset"`
	Limit   int    `json:"limit"`
}

func NewFSTool(rootDir string) *FSTool {
	return &FSTool{rootDir: rootDir}
}

func (f *FSTool) Name() string {
	return "fs"
}

func (f *FSTool) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	var in FSInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("fs: %w", err)
	}

	switch in.Action {
	case "read":
		return f.read(ctx, &in)
	case "write":
		return f.write(ctx, &in)
	case "edit":
		return f.edit(ctx, &in)
	case "glob":
		return f.glob(ctx, &in)
	case "mkdir":
		return f.mkdir(ctx, &in)
	case "stat":
		return f.stat(ctx, &in)
	default:
		return nil, fmt.Errorf("fs: unknown action %q", in.Action)
	}
}

func (f *FSTool) resolve(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("fs: path is required")
	}
	abs := filepath.Join(f.rootDir, path)
	abs = filepath.Clean(abs)
	root, err := filepath.Abs(f.rootDir)
	if err != nil {
		return "", fmt.Errorf("fs: %w", err)
	}
	if !strings.HasPrefix(abs, root) {
		return "", fmt.Errorf("fs: path traversal rejected: %s", path)
	}
	return abs, nil
}

func (f *FSTool) read(_ context.Context, in *FSInput) (json.RawMessage, error) {
	resolved, err := f.resolve(in.Path)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(resolved)
	if err != nil {
		return nil, fmt.Errorf("fs read: %w", err)
	}

	lines := strings.Split(string(data), "\n")
	start := 0
	end := len(lines)

	if in.Offset > 0 {
		start = in.Offset - 1
		if start > len(lines) {
			start = len(lines)
		}
	}
	if in.Limit > 0 {
		e := start + in.Limit
		if e < end {
			end = e
		}
	}

	selected := lines[start:end]
	var numbered strings.Builder
	for i, line := range selected {
		numbered.WriteString(fmt.Sprintf("%d: %s\n", start+i+1, line))
	}

	result := ToolResult{Output: numbered.String(), Success: true}
	raw, _ := json.Marshal(result)
	return raw, nil
}

func (f *FSTool) write(_ context.Context, in *FSInput) (json.RawMessage, error) {
	resolved, err := f.resolve(in.Path)
	if err != nil {
		return nil, err
	}

	dir := filepath.Dir(resolved)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("fs write: %w", err)
	}

	if err := os.WriteFile(resolved, []byte(in.Content), 0o644); err != nil {
		return nil, fmt.Errorf("fs write: %w", err)
	}

	result := ToolResult{Output: fmt.Sprintf("wrote %s", in.Path), Success: true}
	raw, _ := json.Marshal(result)
	return raw, nil
}

func (f *FSTool) edit(_ context.Context, in *FSInput) (json.RawMessage, error) {
	resolved, err := f.resolve(in.Path)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(resolved)
	if err != nil {
		return nil, fmt.Errorf("fs edit: %w", err)
	}

	content := string(data)
	count := strings.Count(content, in.Old)
	if count == 0 {
		return nil, fmt.Errorf("fs edit: old string not found")
	}
	if count > 1 {
		return nil, fmt.Errorf("fs edit: old string found %d times, expected exactly 1", count)
	}

	content = strings.Replace(content, in.Old, in.New, 1)

	if err := os.WriteFile(resolved, []byte(content), 0o644); err != nil {
		return nil, fmt.Errorf("fs edit: %w", err)
	}

	result := ToolResult{Output: fmt.Sprintf("edited %s", in.Path), Success: true}
	raw, _ := json.Marshal(result)
	return raw, nil
}

func (f *FSTool) glob(_ context.Context, in *FSInput) (json.RawMessage, error) {
	resolved := filepath.Join(f.rootDir, in.Pattern)
	matches, err := filepath.Glob(resolved)
	if err != nil {
		return nil, fmt.Errorf("fs glob: %w", err)
	}

	relPaths := make([]string, len(matches))
	for i, m := range matches {
		rel, err := filepath.Rel(f.rootDir, m)
		if err != nil {
			return nil, fmt.Errorf("fs glob: %w", err)
		}
		relPaths[i] = rel
	}

	out, _ := json.Marshal(relPaths)
	result := ToolResult{Output: string(out), Success: true}
	raw, _ := json.Marshal(result)
	return raw, nil
}

func (f *FSTool) mkdir(_ context.Context, in *FSInput) (json.RawMessage, error) {
	resolved, err := f.resolve(in.Path)
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(resolved, 0o755); err != nil {
		return nil, fmt.Errorf("fs mkdir: %w", err)
	}

	result := ToolResult{Output: fmt.Sprintf("created directory %s", in.Path), Success: true}
	raw, _ := json.Marshal(result)
	return raw, nil
}

type statOutput struct {
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	Mode    string `json:"mode"`
	ModTime string `json:"mod_time"`
	IsDir   bool   `json:"is_dir"`
}

func (f *FSTool) stat(_ context.Context, in *FSInput) (json.RawMessage, error) {
	resolved, err := f.resolve(in.Path)
	if err != nil {
		return nil, err
	}

	info, err := os.Stat(resolved)
	if err != nil {
		return nil, fmt.Errorf("fs stat: %w", err)
	}

	out := statOutput{
		Path:    in.Path,
		Size:    info.Size(),
		Mode:    info.Mode().String(),
		ModTime: info.ModTime().Format("2006-01-02T15:04:05Z07:00"),
		IsDir:   info.IsDir(),
	}

	raw, _ := json.Marshal(out)
	result := ToolResult{Output: string(raw), Success: true}
	res, _ := json.Marshal(result)
	return res, nil
}

func isBinaryFile(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()

	buf := make([]byte, 512)
	n, err := f.Read(buf)
	if err != nil {
		return false, err
	}

	for _, b := range buf[:n] {
		if b == 0 {
			return true, nil
		}
	}
	return false, nil
}

func fileInfo(name string) (fs.FileInfo, error) {
	return os.Stat(name)
}

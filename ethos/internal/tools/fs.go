package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	root = filepath.Clean(root)
	// Compare via filepath.Rel: "../" prefix indicates escape, "."
	// is the root itself (allowed), anything else is a child. The
	// prior strings.HasPrefix(abs, root) check let "/home/user/wo"
	// match against "/home/user/work" — classic prefix-without-
	// separator bug.
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return "", fmt.Errorf("fs: path traversal rejected: %s", path)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("fs: path traversal rejected: %s", path)
	}
	return abs, nil
}

// largeFileByteThreshold is the master plan §4.4 ">25K tokens" line.
// ~4 chars per token → 100K bytes. Files at or above this AND opened
// without explicit Offset/Limit return a disk reference instead of
// flooding context. Callers can still drill in with ranged reads.
const largeFileByteThreshold = 100 * 1024

// rangedReadLineCap bounds an Offset+Limit slice so a user who asks
// for "lines 1..1_000_000" still gets bounded output. Picked to keep
// the result under ~150K chars in the worst case (avg 150 chars/line
// × 1000 lines).
const rangedReadLineCap = 1000

// mediumFileLineThreshold triggers the §4.4 "grep first" nudge: when
// a full-file read returns more than this many lines, append a soft
// suggestion to use grep + ranged reads next time. NOT blocking.
const mediumFileLineThreshold = 200

func (f *FSTool) read(_ context.Context, in *FSInput) (json.RawMessage, error) {
	resolved, err := f.resolve(in.Path)
	if err != nil {
		return nil, err
	}

	// Open ONCE and reuse the file handle for both the size check and
	// the actual read. The old Stat-then-ReadFile sequence was a TOCTOU
	// window — a symlink swap between the two syscalls could route the
	// read to a different file than the one whose size we approved.
	// Using a single FD pinned the inode for the duration of the check.
	fh, err := os.Open(resolved)
	if err != nil {
		return nil, fmt.Errorf("fs read: %w", err)
	}
	defer fh.Close()
	info, err := fh.Stat()
	if err != nil {
		return nil, fmt.Errorf("fs read: %w", err)
	}
	// §4.4 large-file disk reference: when no ranged read is requested
	// and the file is huge, return a structured peek instead of the
	// whole contents. Agent can then call back with Offset/Limit or
	// use grep to drill in. Wholesale read on a 50K-line file would
	// swamp the context window in one tool call.
	if in.Offset == 0 && in.Limit == 0 && info.Size() >= largeFileByteThreshold {
		return largeFileReference(resolved, info.Size())
	}

	data, err := io.ReadAll(fh)
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
	// Hard cap the ranged slice regardless of what the user asked for,
	// so a careless Limit=999999 doesn't reintroduce the unbounded
	// read we just guarded against.
	if end-start > rangedReadLineCap {
		end = start + rangedReadLineCap
	}

	selected := lines[start:end]
	var numbered strings.Builder
	for i, line := range selected {
		numbered.WriteString(fmt.Sprintf("%d: %s\n", start+i+1, line))
	}
	// §4.4 grep-first nudge: when the user did a full read of a
	// medium-sized file, hint that grep + ranged would have been
	// cheaper. Non-blocking; the read still succeeds.
	if in.Offset == 0 && in.Limit == 0 && len(lines) > mediumFileLineThreshold {
		numbered.WriteString(fmt.Sprintf(
			"\n# tip (§4.4): file is %d lines — next time prefer `grep -n` + Offset/Limit when looking for something specific\n",
			len(lines)))
	}

	result := ToolResult{Output: numbered.String(), Success: true}
	raw, _ := json.Marshal(result)
	return raw, nil
}

// largeFileReference returns a compact disk-reference payload for
// files past the size threshold. Includes head/tail peeks so the
// agent can decide where to drill in without a second read.
func largeFileReference(path string, size int64) (json.RawMessage, error) {
	const peekLines = 20
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("fs read: large-file peek: %w", err)
	}
	lines := strings.Split(string(data), "\n")

	var b strings.Builder
	fmt.Fprintf(&b, "FILE TOO LARGE FOR INLINE READ — %d bytes, %d lines\n", size, len(lines))
	fmt.Fprintf(&b, "Path: %s\n", path)
	b.WriteString("This file exceeds the inline-read threshold (~25K tokens). ")
	b.WriteString("Call fs.read again with Offset+Limit to drill into a range, ")
	b.WriteString("or use grep/search tools to find what you need.\n\n")

	head := peekLines
	if head > len(lines) {
		head = len(lines)
	}
	b.WriteString("--- head (first 20 lines) ---\n")
	for i := 0; i < head; i++ {
		fmt.Fprintf(&b, "%d: %s\n", i+1, lines[i])
	}

	if len(lines) > 2*peekLines {
		tailStart := len(lines) - peekLines
		fmt.Fprintf(&b, "\n... %d lines omitted ...\n\n", tailStart-head)
		b.WriteString("--- tail (last 20 lines) ---\n")
		for i := tailStart; i < len(lines); i++ {
			fmt.Fprintf(&b, "%d: %s\n", i+1, lines[i])
		}
	}

	result := ToolResult{Output: b.String(), Success: true}
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
	if in.Pattern == "" {
		return nil, fmt.Errorf("fs glob: pattern is required")
	}
	// Reject patterns that try to break out of root before even touching
	// the filesystem. resolve() does the same for plain paths; glob has
	// its own escape hatches (`..`, absolute paths, symlinks resolved by
	// filepath.Glob) so we belt-and-braces: cleaned-joined path must stay
	// under root, AND we re-check each match below.
	joined := filepath.Clean(filepath.Join(f.rootDir, in.Pattern))
	root, err := filepath.Abs(f.rootDir)
	if err != nil {
		return nil, fmt.Errorf("fs glob: %w", err)
	}
	root = filepath.Clean(root)
	// HasPrefix lies when one root is a prefix of another sibling
	// (e.g. root=/home/user/wo joined=/home/user/work/etc → "wo" prefix
	// of "work" returns true even though work/etc is outside wo).
	// filepath.Rel is the only safe containment check.
	if rel, rerr := filepath.Rel(root, joined); rerr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("fs glob: path traversal rejected: %s", in.Pattern)
	}
	matches, err := filepath.Glob(joined)
	if err != nil {
		return nil, fmt.Errorf("fs glob: %w", err)
	}

	// Filter matches whose resolved absolute path escaped root (e.g. via
	// a symlink inside the tree pointing outside it). Surface only the
	// in-root subset. Use EvalSymlinks so a symlink that targets outside
	// root is rejected even when the link itself lives inside.
	relPaths := make([]string, 0, len(matches))
	for _, m := range matches {
		absM, err := filepath.Abs(m)
		if err != nil {
			continue
		}
		// Resolve symlinks for the containment check. Failure → drop;
		// the file may have been deleted between Glob and here.
		resolved, rerr := filepath.EvalSymlinks(absM)
		if rerr != nil {
			continue
		}
		rel, err := filepath.Rel(root, resolved)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		// Report the cleaned, root-relative form computed from the
		// resolved path so the output is consistent with the check.
		relPaths = append(relPaths, rel)
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

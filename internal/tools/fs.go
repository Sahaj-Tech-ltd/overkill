package tools

import (
	"bufio"
	"bytes"
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

// The byte cap also bounds pathological files containing a single huge line.
const readOutputByteCap = 100 * 1024
const readLineByteCap = 4096

// streamLines retains a bounded prefix of each line, including huge lines.
// A trailing newline has an empty final line, preserving original numbering.
func streamLines(ctx context.Context, src io.Reader, visit func(int, string) bool) error {
	reader := bufio.NewReaderSize(src, 32*1024)
	line := make([]byte, 0, readLineByteCap)
	number, truncated := 1, false
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		fragment, err := reader.ReadSlice('\n')
		finished := err != bufio.ErrBufferFull
		fragment = bytes.TrimSuffix(fragment, []byte{'\n'})
		room := readLineByteCap - len(line)
		if len(fragment) > room {
			line = append(line, fragment[:room]...)
			truncated = true
		} else {
			line = append(line, fragment...)
		}
		if finished {
			text := string(line)
			if truncated {
				text += " … [line truncated]"
			}
			if !visit(number, text) {
				return nil
			}
			number++
			line = line[:0]
			truncated = false
		}
		if err == io.EOF {
			return nil
		}
		if err != nil && err != bufio.ErrBufferFull {
			return err
		}
	}
}

func (f *FSTool) read(ctx context.Context, in *FSInput) (json.RawMessage, error) {
	root, name, err := openWorkspace(f.rootDir, in.Path)
	if err != nil {
		return nil, fmt.Errorf("fs: %w", err)
	}
	defer root.Close()
	fh, err := root.Open(name)
	if err != nil {
		return nil, fmt.Errorf("fs read: %w", err)
	}
	defer fh.Close()
	info, err := fh.Stat()
	if err != nil {
		return nil, fmt.Errorf("fs read: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("fs read: not a regular file")
	}
	if in.Offset == 0 && in.Limit == 0 && info.Size() >= largeFileByteThreshold {
		return largeFileReference(ctx, fh, filepath.Join(f.rootDir, name), info.Size())
	}
	start := in.Offset
	if start < 1 {
		start = 1
	}
	limit := in.Limit
	if limit <= 0 || limit > rangedReadLineCap {
		limit = rangedReadLineCap
	}
	var out strings.Builder
	selected, total := 0, 0
	err = streamLines(ctx, fh, func(n int, line string) bool {
		total = n
		if n < start {
			return true
		}
		text := fmt.Sprintf("%d: %s\n", n, line)
		if out.Len()+len(text) > readOutputByteCap {
			out.WriteString("# output truncated; use Offset/Limit to continue\n")
			return false
		}
		out.WriteString(text)
		selected++
		return selected < limit
	})
	if err != nil {
		return nil, fmt.Errorf("fs read: %w", err)
	}
	if in.Offset == 0 && in.Limit == 0 && total > mediumFileLineThreshold {
		out.WriteString("\n# tip (§4.4): prefer grep + Offset/Limit when looking for something specific\n")
	}
	return json.Marshal(ToolResult{Output: out.String(), Success: true})
}

// largeFileReference scans the original descriptor with fixed memory: twenty
// head lines and a twenty-line ring for the tail, each with a byte limit.
func largeFileReference(ctx context.Context, fh io.Reader, path string, size int64) (json.RawMessage, error) {
	const peekLines = 20
	head := make([]string, 0, peekLines)
	var tail [peekLines]string
	total := 0
	err := streamLines(ctx, fh, func(n int, line string) bool {
		total = n
		if n <= peekLines {
			head = append(head, line)
		}
		tail[(n-1)%peekLines] = line
		return true
	})
	if err != nil {
		return nil, fmt.Errorf("fs read: large-file peek: %w", err)
	}
	var out strings.Builder
	fmt.Fprintf(&out, "FILE TOO LARGE FOR INLINE READ — %d bytes, %d lines\nPath: %s\n", size, total, path)
	out.WriteString("Call fs.read again with Offset+Limit to drill into a range, or use grep/search.\n\n--- head (first 20 lines) ---\n")
	for i, line := range head {
		fmt.Fprintf(&out, "%d: %s\n", i+1, line)
	}
	if total > 2*peekLines {
		fmt.Fprintf(&out, "\n... %d lines omitted ...\n\n--- tail (last 20 lines) ---\n", total-2*peekLines)
		for n := total - peekLines + 1; n <= total; n++ {
			fmt.Fprintf(&out, "%d: %s\n", n, tail[(n-1)%peekLines])
		}
	}
	return json.Marshal(ToolResult{Output: out.String(), Success: true})
}

func (f *FSTool) write(_ context.Context, in *FSInput) (json.RawMessage, error) {
	root, name, err := openWorkspace(f.rootDir, in.Path)
	if err != nil {
		return nil, fmt.Errorf("fs: %w", err)
	}
	defer root.Close()

	dir := filepath.Dir(name)
	if err := root.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("fs write: %w", err)
	}

	if err := atomicWorkspaceWrite(root, name, []byte(in.Content)); err != nil {
		return nil, fmt.Errorf("fs write: %w", err)
	}

	result := ToolResult{Output: fmt.Sprintf("wrote %s", in.Path), Success: true}
	raw, _ := json.Marshal(result)
	return raw, nil
}

func (f *FSTool) edit(_ context.Context, in *FSInput) (json.RawMessage, error) {
	root, name, err := openWorkspace(f.rootDir, in.Path)
	if err != nil {
		return nil, fmt.Errorf("fs: %w", err)
	}
	defer root.Close()

	data, err := root.ReadFile(name)
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

	if err := atomicWorkspaceWrite(root, name, []byte(content)); err != nil {
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
	root, pattern, err := openWorkspace(f.rootDir, in.Pattern)
	if err != nil {
		return nil, fmt.Errorf("fs glob: %w", err)
	}
	defer root.Close()
	matches, err := fs.Glob(root.FS(), filepath.ToSlash(pattern))
	if err != nil {
		return nil, fmt.Errorf("fs glob: %w", err)
	}
	relPaths := make([]string, 0, len(matches))
	for _, match := range matches {
		if _, err := root.Stat(match); err == nil {
			relPaths = append(relPaths, match)
		}
	}

	out, _ := json.Marshal(relPaths)
	result := ToolResult{Output: string(out), Success: true}
	raw, _ := json.Marshal(result)
	return raw, nil
}

func (f *FSTool) mkdir(_ context.Context, in *FSInput) (json.RawMessage, error) {
	root, name, err := openWorkspace(f.rootDir, in.Path)
	if err != nil {
		return nil, fmt.Errorf("fs: %w", err)
	}
	defer root.Close()

	if err := root.MkdirAll(name, 0o750); err != nil {
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
	root, name, err := openWorkspace(f.rootDir, in.Path)
	if err != nil {
		return nil, fmt.Errorf("fs: %w", err)
	}
	defer root.Close()

	info, err := root.Stat(name)
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

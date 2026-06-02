// Package tools — `patch` tool: applies a unified diff to a file with strict
// context validation.
//
// The format accepted is the standard unified diff hunk:
//
//	@@ -<oldStart>,<oldCount> +<newStart>,<newCount> @@
//	 context line
//	-removed line
//	+added line
//
// Multiple hunks are applied in file order. A context-line mismatch fails the
// whole apply (no partial writes).
package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// PatchTool implements Tool. It validates and applies unified diffs.
type PatchTool struct {
	rootDir string
}

// PatchInput is the JSON schema accepted by the tool.
type PatchInput struct {
	Path  string `json:"path"`
	Patch string `json:"patch"`
}

// PatchOutput is the JSON schema returned by the tool.
type PatchOutput struct {
	Path         string `json:"path"`
	HunksApplied int    `json:"hunks_applied"`
	Result       string `json:"result"` // resulting file content
}

// NewPatchTool creates a patch tool rooted at rootDir. Paths are resolved
// relative to it.
func NewPatchTool(rootDir string) *PatchTool {
	return &PatchTool{rootDir: rootDir}
}

// Name returns the tool identifier.
func (p *PatchTool) Name() string { return "patch" }

// Execute parses the patch and applies it.
func (p *PatchTool) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	var in PatchInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("patch: parse input: %w", err)
	}
	if in.Path == "" {
		return nil, fmt.Errorf("patch: path is required")
	}
	if in.Patch == "" {
		return nil, fmt.Errorf("patch: patch is required")
	}

	// Path-traversal guard matching tools/fs.go semantics: clean the
	// path, compare via filepath.Rel against the cleaned rootDir,
	// reject any path that escapes the root (rel starts with "..").
	// Absolute paths are resolved + checked the same way — prior
	// code accepted `/etc/passwd` and `../../etc/passwd` unconditionally.
	full := in.Path
	if !filepath.IsAbs(full) {
		full = filepath.Join(p.rootDir, in.Path)
	}
	full = filepath.Clean(full)
	root, rerr := filepath.Abs(p.rootDir)
	if rerr != nil {
		return nil, fmt.Errorf("patch: resolve root: %w", rerr)
	}
	root = filepath.Clean(root)
	rel, relErr := filepath.Rel(root, full)
	if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("patch: path traversal rejected: %s", in.Path)
	}
	src, err := os.ReadFile(full)
	if err != nil {
		return nil, fmt.Errorf("patch: read %s: %w", full, err)
	}
	hunks, err := ParseUnifiedDiff(in.Patch)
	if err != nil {
		return nil, fmt.Errorf("patch: parse diff: %w", err)
	}
	out, err := ApplyHunks(string(src), hunks)
	if err != nil {
		return nil, fmt.Errorf("patch: apply: %w", err)
	}
	// Re-read between apply and write to detect external modification
	// in the window. The old read-apply-write sequence let a concurrent
	// editor's changes get silently overwritten because our patch was
	// computed against stale content. If the file has moved on, refuse
	// and surface the diff so the caller can retry on fresh content.
	current, err := os.ReadFile(full)
	if err != nil {
		return nil, fmt.Errorf("patch: re-read %s: %w", full, err)
	}
	if !bytes.Equal(current, src) {
		return nil, fmt.Errorf("patch: %s was modified between read and write; aborting to avoid clobbering external changes", full)
	}
	if err := os.WriteFile(full, []byte(out), 0o600); err != nil {
		return nil, fmt.Errorf("patch: write %s: %w", full, err)
	}
	return json.Marshal(PatchOutput{
		Path:         in.Path,
		HunksApplied: len(hunks),
		Result:       out,
	})
}

// Hunk is one parsed @@ ... @@ block.
type Hunk struct {
	OldStart int
	OldCount int
	NewStart int
	NewCount int
	Lines    []string // each starts with ' ', '-', or '+'
}

// ParseUnifiedDiff parses a unified diff string into hunks. File header lines
// (--- / +++) are tolerated and skipped.
func ParseUnifiedDiff(patch string) ([]Hunk, error) {
	lines := strings.Split(patch, "\n")
	// Strip a single trailing empty line caused by the patch ending in '\n'.
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	var hunks []Hunk
	var cur *Hunk
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "+++ ") {
			continue
		}
		if strings.HasPrefix(line, "@@") {
			if cur != nil {
				hunks = append(hunks, *cur)
			}
			h, err := parseHunkHeader(line)
			if err != nil {
				return nil, err
			}
			cur = &h
			continue
		}
		if cur == nil {
			// allow leading blank lines before the first hunk
			if strings.TrimSpace(line) == "" {
				continue
			}
			return nil, fmt.Errorf("unexpected line before first hunk: %q", line)
		}
		if line == "" {
			cur.Lines = append(cur.Lines, " ")
			continue
		}
		switch line[0] {
		case ' ', '-', '+':
			cur.Lines = append(cur.Lines, line)
		case '\\':
			// "\ No newline at end of file" — ignore.
		default:
			return nil, fmt.Errorf("malformed hunk line: %q", line)
		}
	}
	if cur != nil {
		hunks = append(hunks, *cur)
	}
	if len(hunks) == 0 {
		return nil, fmt.Errorf("no hunks found")
	}
	return hunks, nil
}

func parseHunkHeader(line string) (Hunk, error) {
	// @@ -oldStart,oldCount +newStart,newCount @@ optional context
	if !strings.HasPrefix(line, "@@") {
		return Hunk{}, fmt.Errorf("not a hunk header: %q", line)
	}
	rest := strings.TrimPrefix(line, "@@")
	idx := strings.Index(rest, "@@")
	if idx < 0 {
		return Hunk{}, fmt.Errorf("malformed hunk header: %q", line)
	}
	header := strings.TrimSpace(rest[:idx])
	parts := strings.Fields(header)
	if len(parts) != 2 {
		return Hunk{}, fmt.Errorf("malformed hunk header: %q", line)
	}
	oldStart, oldCount, err := parseRange(parts[0], '-')
	if err != nil {
		return Hunk{}, err
	}
	newStart, newCount, err := parseRange(parts[1], '+')
	if err != nil {
		return Hunk{}, err
	}
	return Hunk{
		OldStart: oldStart,
		OldCount: oldCount,
		NewStart: newStart,
		NewCount: newCount,
	}, nil
}

func parseRange(s string, sigil byte) (start, count int, err error) {
	if len(s) == 0 || s[0] != sigil {
		return 0, 0, fmt.Errorf("expected %q prefix on %q", string(sigil), s)
	}
	body := s[1:]
	startStr, countStr, hasComma := strings.Cut(body, ",")
	start, err = strconv.Atoi(startStr)
	if err != nil {
		return 0, 0, fmt.Errorf("bad start %q: %w", startStr, err)
	}
	if hasComma {
		count, err = strconv.Atoi(countStr)
		if err != nil {
			return 0, 0, fmt.Errorf("bad count %q: %w", countStr, err)
		}
	} else {
		count = 1
	}
	return start, count, nil
}

// ApplyHunks applies hunks to src and returns the resulting content. A context
// mismatch in any hunk fails the whole operation.
func ApplyHunks(src string, hunks []Hunk) (string, error) {
	srcLines := splitLinesPreserve(src)

	// Apply hunks in order. We track an offset between the original line
	// numbers and the current state (since prior hunks may have grown/shrunk
	// the file).
	out := append([]string(nil), srcLines...)
	offset := 0
	for hi, h := range hunks {
		idx := h.OldStart - 1 + offset
		if idx < 0 {
			return "", fmt.Errorf("hunk %d: oldStart %d out of range", hi+1, h.OldStart)
		}
		// Walk the hunk lines, validating context/removed lines, building a
		// replacement slice.
		replaceLen := 0 // number of source lines this hunk consumes
		var newSegment []string
		cursor := idx
		for _, line := range h.Lines {
			tag := line[0]
			body := line[1:]
			switch tag {
			case ' ':
				if cursor >= len(out) {
					return "", fmt.Errorf("hunk %d: context past EOF", hi+1)
				}
				if out[cursor] != body {
					return "", fmt.Errorf("hunk %d: context mismatch at line %d: have %q want %q",
						hi+1, cursor+1, out[cursor], body)
				}
				newSegment = append(newSegment, body)
				cursor++
				replaceLen++
			case '-':
				if cursor >= len(out) {
					return "", fmt.Errorf("hunk %d: removal past EOF", hi+1)
				}
				if out[cursor] != body {
					return "", fmt.Errorf("hunk %d: removal mismatch at line %d: have %q want %q",
						hi+1, cursor+1, out[cursor], body)
				}
				cursor++
				replaceLen++
			case '+':
				newSegment = append(newSegment, body)
			}
		}
		// Splice newSegment in for [idx, idx+replaceLen).
		end := idx + replaceLen
		out = append(out[:idx], append(append([]string(nil), newSegment...), out[end:]...)...)
		offset += len(newSegment) - replaceLen
	}

	return joinLinesPreserve(out, src), nil
}

// splitLinesPreserve splits on '\n' but does NOT drop the trailing empty entry,
// so that joining preserves the trailing newline (if any).
func splitLinesPreserve(s string) []string {
	if s == "" {
		return []string{""}
	}
	return strings.Split(s, "\n")
}

func joinLinesPreserve(lines []string, original string) string {
	out := strings.Join(lines, "\n")
	// If the original ended without a newline and our join added a trailing
	// empty line, trim it; if the original ended with a newline and we lost
	// it, add it back.
	hadTrailingNL := strings.HasSuffix(original, "\n")
	hasTrailingNL := strings.HasSuffix(out, "\n")
	if hadTrailingNL && !hasTrailingNL {
		out += "\n"
	}
	if !hadTrailingNL && hasTrailingNL {
		out = strings.TrimSuffix(out, "\n")
	}
	return out
}

// Package tools — additional output compressors covering tools beyond the
// shell/grep/git core. Each compressor is conservative: it only trims when
// the saving is meaningful, and never alters the JSON envelope structure.
//
// Registered automatically via NewCompressorRegistry.
package tools

import (
	"encoding/json"
	"fmt"
	"strings"
)

// largeOutputThreshold is the byte cap above which a generic head+tail trim
// is applied to a tool's `output` string field. Below this we leave content
// alone — short outputs aren't a budget problem.
const largeOutputThreshold = 8 * 1024

// HeadTailCompressor is a generic compressor that keeps the first and last
// portion of a long string-`output` ToolResult and drops the middle. Used
// for fs, web, browser_text/markdown, lsp_*, pty_shell, and patch.
type HeadTailCompressor struct {
	tool      string
	headBytes int
	tailBytes int
}

// NewHeadTailCompressor builds a HeadTailCompressor that fires when the
// underlying ToolResult.Output exceeds head+tail+1024 bytes.
func NewHeadTailCompressor(tool string, head, tail int) *HeadTailCompressor {
	if head <= 0 {
		head = 4 * 1024
	}
	if tail <= 0 {
		tail = 2 * 1024
	}
	return &HeadTailCompressor{tool: tool, headBytes: head, tailBytes: tail}
}

func (h *HeadTailCompressor) ToolName() string { return h.tool }

func (h *HeadTailCompressor) Compress(output json.RawMessage) (json.RawMessage, int, error) {
	originalLen := len(output)
	if originalLen < largeOutputThreshold {
		return output, 0, nil
	}

	// Try the canonical ToolResult shape first.
	var tr ToolResult
	if err := json.Unmarshal(output, &tr); err == nil && tr.Output != "" {
		if len(tr.Output) <= h.headBytes+h.tailBytes+1024 {
			return output, 0, nil
		}
		tr.Output = headTail(tr.Output, h.headBytes, h.tailBytes)
		raw, err := json.Marshal(tr)
		if err != nil {
			return output, 0, nil
		}
		saved := originalLen - len(raw)
		if saved <= 0 {
			return output, 0, nil
		}
		return raw, saved, nil
	}

	// Fallback: dump generic JSON object and look for an "output" or
	// "content" string field.
	var generic map[string]any
	if err := json.Unmarshal(output, &generic); err != nil {
		return output, 0, nil
	}
	for _, key := range []string{"output", "content", "text", "markdown", "html"} {
		if v, ok := generic[key].(string); ok && len(v) > h.headBytes+h.tailBytes+1024 {
			generic[key] = headTail(v, h.headBytes, h.tailBytes)
			raw, err := json.Marshal(generic)
			if err != nil {
				return output, 0, nil
			}
			saved := originalLen - len(raw)
			if saved <= 0 {
				return output, 0, nil
			}
			return raw, saved, nil
		}
	}
	return output, 0, nil
}

// headTail keeps head bytes from the start, tail bytes from the end, with
// a "[truncated N chars]" marker between them. Tries to cut on a newline to
// avoid mid-token slicing.
func headTail(s string, head, tail int) string {
	if len(s) <= head+tail {
		return s
	}
	headPart := s[:head]
	if i := strings.LastIndexByte(headPart, '\n'); i > head/2 {
		headPart = headPart[:i]
	}
	tailPart := s[len(s)-tail:]
	if i := strings.IndexByte(tailPart, '\n'); i >= 0 && i < tail/2 {
		tailPart = tailPart[i+1:]
	}
	skipped := len(s) - len(headPart) - len(tailPart)
	return fmt.Sprintf("%s\n... [truncated %d chars]\n%s", headPart, skipped, tailPart)
}

// PatchCompressor strips the file contents from a successful patch result,
// keeping only the per-file action summary. Failed patches keep their full
// error context so the agent can debug.
type PatchCompressor struct{}

func (PatchCompressor) ToolName() string { return "patch" }

func (PatchCompressor) Compress(output json.RawMessage) (json.RawMessage, int, error) {
	originalLen := len(output)
	if originalLen < 1024 {
		return output, 0, nil
	}
	var generic map[string]any
	if err := json.Unmarshal(output, &generic); err != nil {
		return output, 0, nil
	}
	// If there's an "error" or "errors" field with content, keep everything.
	if e, ok := generic["error"].(string); ok && e != "" {
		return output, 0, nil
	}
	// Drop heavy "result" / "patch" payloads (PatchOutput fields: path, hunks_applied, result).
	dropped := false
	for _, k := range []string{"result", "patch"} {
		if v, ok := generic[k].(string); ok && len(v) > 1024 {
			generic[k] = fmt.Sprintf("[truncated %d chars]", len(v))
			dropped = true
		}
	}
	if !dropped {
		return output, 0, nil
	}
	raw, err := json.Marshal(generic)
	if err != nil {
		return output, 0, nil
	}
	saved := originalLen - len(raw)
	if saved <= 0 {
		return output, 0, nil
	}
	return raw, saved, nil
}

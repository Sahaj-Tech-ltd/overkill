// Package verify — default registry wiring + tool-input path
// extraction.
//
// Two pieces glued here:
//
//  1. DefaultRegistry() returns a Registry pre-loaded with every
//     built-in verifier. The agent boot path calls this once and
//     hands the registry to the dispatcher.
//
//  2. ExtractWritePaths inspects a tool call's input JSON for keys
//     that name a written file (path, file_path, target, etc.) so
//     the dispatcher can run verifiers without each write tool
//     opting in. New write tools work out of the box as long as
//     they use one of the conventional key names.
package verify

import (
	"encoding/json"
	"path/filepath"
	"strings"
)

// DefaultRegistry returns a registry pre-loaded with built-in
// verifiers. Callers can Register more or override existing ones.
func DefaultRegistry() *Registry {
	r := NewRegistry()
	r.Register(".go", NewGoVerifier())
	r.Register(".toml", NewTOMLVerifier())
	r.Register(".json", NewJSONVerifier())
	r.Register(".yaml", NewYAMLVerifier())
	r.Register(".yml", NewYAMLVerifier())
	return r
}

// pathKeys is the set of input-JSON keys that name a file the tool
// wrote. Centralised so a single audit can update every recognised
// shape. We accept the common variants because different tools
// (fs_write vs patch vs edit) use different conventions.
var pathKeys = map[string]bool{
	"path":      true,
	"file_path": true,
	"filepath":  true,
	"target":    true,
	"file":      true,
}

// WriteToolNames is the set of tool names whose calls produce a
// post-write verification pass. Maintained as a denylist-of-everything-
// else rather than scraping the registry, because some non-write
// tools take a "path" argument (fs_read) and we'd otherwise verify
// reads too.
var WriteToolNames = map[string]bool{
	"fs_write":   true,
	"fs_edit":    true,
	"edit":       true,
	"write":      true,
	"patch":      true,
	"apply":      true,
	"create":     true,
	"slice":      true, // overkill's range-write tool
}

// IsWriteTool reports whether toolName is recognised as a write
// tool whose output should be verified.
func IsWriteTool(toolName string) bool {
	return WriteToolNames[strings.ToLower(toolName)]
}

// ExtractWritePaths scans the tool input JSON for path-typed keys.
// Returns a deduplicated, cleaned list of paths the tool wrote to.
// Caller resolves them against cwd before passing to VerifyPaths.
//
// Multi-file payloads (e.g. patch with `files: [{path: "..."}, ...]`)
// are handled by walking the JSON tree, not just top-level keys.
// Anything that LOOKS like a path under one of pathKeys gets
// collected.
func ExtractWritePaths(toolName string, input json.RawMessage) []string {
	if !IsWriteTool(toolName) || len(input) == 0 {
		return nil
	}
	var root any
	if err := json.Unmarshal(input, &root); err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	walkForPaths(root, &out, seen)
	return out
}

// walkForPaths recursively walks JSON. When it finds a string value
// under a pathKeys key, it adds the value to out. Arrays of objects
// are traversed so multi-file payloads work.
func walkForPaths(v any, out *[]string, seen map[string]bool) {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if pathKeys[strings.ToLower(k)] {
				if s, ok := val.(string); ok && s != "" {
					clean := filepath.Clean(s)
					if !seen[clean] {
						seen[clean] = true
						*out = append(*out, clean)
					}
				}
			}
			walkForPaths(val, out, seen)
		}
	case []any:
		for _, item := range t {
			walkForPaths(item, out, seen)
		}
	}
}

// Package tools — overkill_introspect lets the agent read auto-generated
// introspection files (CODEBASE.md, MODEL_CARD.md, KNOWN_ISSUES.md,
// ARCHITECTURE.md) from ~/.overkill/introspection/ on demand. Read-only.
// See master plan §4.18.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// IntrospectTool exposes the introspection read-path.
type IntrospectTool struct {
	dir string // typically ~/.overkill/introspection
}

// maxIntrospectChars is the cap before truncating responses. Files above this
// get a tail-note pointing at the on-disk path.
const maxIntrospectChars = 30 * 1024

// topicFiles maps user-facing topic names to the on-disk file. Only these
// four topics are accepted; any other input (including paths with "..", "/",
// etc.) is rejected.
var topicFiles = map[string]string{
	"codebase":     "CODEBASE.md",
	"model":        "MODEL_CARD.md",
	"issues":       "KNOWN_ISSUES.md",
	"architecture": "ARCHITECTURE.md",
}

// NewIntrospectTool wires the introspection dir. Pass empty to default to
// ~/.overkill/introspection.
func NewIntrospectTool(dir string) *IntrospectTool {
	if dir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			dir = filepath.Join(home, ".overkill", "introspection")
		}
	}
	return &IntrospectTool{dir: dir}
}

func (t *IntrospectTool) Name() string { return "overkill_introspect" }

type introspectInput struct {
	Topic string `json:"topic,omitempty"`
}

func (t *IntrospectTool) Execute(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var in introspectInput
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &in); err != nil {
			return nil, fmt.Errorf("overkill_introspect: %w", err)
		}
	}

	topic := strings.ToLower(strings.TrimSpace(in.Topic))

	// Empty topic: list available files.
	if topic == "" {
		return t.list()
	}

	file, ok := topicFiles[topic]
	if !ok {
		known := make([]string, 0, len(topicFiles))
		for k := range topicFiles {
			known = append(known, k)
		}
		sort.Strings(known)
		return nil, fmt.Errorf("overkill_introspect: unknown topic %q (known: %s)", in.Topic, strings.Join(known, ", "))
	}

	path := filepath.Join(t.dir, file)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			result := ToolResult{
				Output:  fmt.Sprintf("no introspection data available for %q — file not yet generated at %s", topic, path),
				Success: true,
			}
			return json.Marshal(result)
		}
		return nil, fmt.Errorf("overkill_introspect: read %s: %w", path, err)
	}

	body := string(data)
	if len(body) > maxIntrospectChars {
		body = body[:maxIntrospectChars] + fmt.Sprintf(
			"\n\n... [truncated at %d chars; full file at %s]",
			maxIntrospectChars, path,
		)
	}

	result := ToolResult{Output: body, Success: true}
	return json.Marshal(result)
}

// list reports which topic files currently exist on disk.
func (t *IntrospectTool) list() (json.RawMessage, error) {
	topics := make([]string, 0, len(topicFiles))
	for k := range topicFiles {
		topics = append(topics, k)
	}
	sort.Strings(topics)

	var b strings.Builder
	b.WriteString("introspection topics (pass as `topic`):\n")
	for _, topic := range topics {
		file := topicFiles[topic]
		path := filepath.Join(t.dir, file)
		status := "missing"
		if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
			status = fmt.Sprintf("%d bytes", fi.Size())
		}
		fmt.Fprintf(&b, "  - %-13s %s (%s)\n", topic, file, status)
	}

	result := ToolResult{Output: b.String(), Success: true}
	return json.Marshal(result)
}

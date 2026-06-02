package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runIntrospect(t *testing.T, tool *IntrospectTool, topic string) (ToolResult, error) {
	t.Helper()
	in, _ := json.Marshal(introspectInput{Topic: topic})
	raw, err := tool.Execute(context.Background(), in)
	if err != nil {
		return ToolResult{}, err
	}
	var r ToolResult
	if jerr := json.Unmarshal(raw, &r); jerr != nil {
		t.Fatalf("unmarshal result: %v", jerr)
	}
	return r, nil
}

func TestIntrospect_KnownTopicsResolve(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"CODEBASE.md":     "codebase body",
		"MODEL_CARD.md":   "model body",
		"KNOWN_ISSUES.md": "issues body",
		"ARCHITECTURE.md": "architecture body",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	tool := NewIntrospectTool(dir)
	cases := map[string]string{
		"codebase":     "codebase body",
		"model":        "model body",
		"issues":       "issues body",
		"architecture": "architecture body",
	}
	for topic, want := range cases {
		t.Run(topic, func(t *testing.T) {
			r, err := runIntrospect(t, tool, topic)
			if err != nil {
				t.Fatalf("execute: %v", err)
			}
			if !r.Success || r.Output != want {
				t.Fatalf("topic %s: got success=%v output=%q want %q", topic, r.Success, r.Output, want)
			}
		})
	}
}

func TestIntrospect_UnknownTopicErrors(t *testing.T) {
	tool := NewIntrospectTool(t.TempDir())
	for _, bad := range []string{"../etc/passwd", "/etc/passwd", "secrets", "codebase.md"} {
		in, _ := json.Marshal(introspectInput{Topic: bad})
		_, err := tool.Execute(context.Background(), in)
		if err == nil {
			t.Errorf("expected error for topic %q", bad)
		}
	}
}

func TestIntrospect_MissingFileGraceful(t *testing.T) {
	tool := NewIntrospectTool(t.TempDir())
	r, err := runIntrospect(t, tool, "codebase")
	if err != nil {
		t.Fatalf("expected nil error for missing file, got %v", err)
	}
	if !r.Success {
		t.Fatalf("expected success=true for graceful missing-file response, got %+v", r)
	}
	if !strings.Contains(r.Output, "no introspection data available") {
		t.Fatalf("expected graceful message, got %q", r.Output)
	}
}

func TestIntrospect_OversizedTruncation(t *testing.T) {
	dir := t.TempDir()
	big := strings.Repeat("A", maxIntrospectChars+500)
	path := filepath.Join(dir, "CODEBASE.md")
	if err := os.WriteFile(path, []byte(big), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	tool := NewIntrospectTool(dir)
	r, err := runIntrospect(t, tool, "codebase")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(r.Output, "truncated") {
		t.Fatalf("expected truncation note, got tail: %q", r.Output[max0(len(r.Output)-200):])
	}
	if !strings.Contains(r.Output, path) {
		t.Fatalf("expected on-disk path in truncation note, got tail: %q", r.Output[max0(len(r.Output)-200):])
	}
	if len(r.Output) >= len(big) {
		t.Fatalf("expected truncated output, got %d chars (input %d)", len(r.Output), len(big))
	}
}

func TestIntrospect_EmptyTopicLists(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "MODEL_CARD.md"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	tool := NewIntrospectTool(dir)
	r, err := runIntrospect(t, tool, "")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	for _, topic := range []string{"codebase", "model", "issues", "architecture"} {
		if !strings.Contains(r.Output, topic) {
			t.Errorf("list missing topic %s: %s", topic, r.Output)
		}
	}
	if !strings.Contains(r.Output, "missing") || !strings.Contains(r.Output, "bytes") {
		t.Errorf("expected status indicators in list output, got: %s", r.Output)
	}
}

func max0(n int) int {
	if n < 0 {
		return 0
	}
	return n
}

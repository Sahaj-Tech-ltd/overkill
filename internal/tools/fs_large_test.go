package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRead_LargeFileReturnsDiskReference exercises the §4.4 large-file
// path. A 150 KB file opened without Offset/Limit should return a
// disk-reference summary, not the full content.
func TestRead_LargeFileReturnsDiskReference(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.go")
	// Build a file just over the threshold (150 KB > 100 KB cap) with
	// numbered lines so head/tail peeks are recognisable.
	var b strings.Builder
	for i := 0; i < 3000; i++ {
		b.WriteString("// line filler for the large-file test, padded to make each line longer\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(path); info.Size() < largeFileByteThreshold {
		t.Fatalf("test fixture too small: %d bytes (need > %d)", info.Size(), largeFileByteThreshold)
	}

	tool := NewFSTool(dir)
	in, _ := json.Marshal(FSInput{Action: "read", Path: "big.go"})
	raw, err := tool.Execute(context.Background(), in)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var res ToolResult
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !strings.Contains(res.Output, "FILE TOO LARGE") {
		t.Errorf("missing too-large header: %q", res.Output[:200])
	}
	if !strings.Contains(res.Output, "head (first 20 lines)") {
		t.Errorf("missing head peek")
	}
	if !strings.Contains(res.Output, "tail (last 20 lines)") {
		t.Errorf("missing tail peek")
	}
	// Crucial: the full body should NOT appear inline. Our threshold
	// is ~100K; the peek (head+tail = 40 lines * ~70 chars + scaffold)
	// should be well under 6KB.
	if len(res.Output) > 8000 {
		t.Errorf("disk-reference payload too large: %d bytes", len(res.Output))
	}
}

// TestRead_RangedReadCapsAtLineLimit: even with Offset+Limit set, the
// returned slice is bounded so a careless Limit=999999 can't reflood
// the context.
func TestRead_RangedReadCapsAtLineLimit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "small.txt")
	var b strings.Builder
	for i := 0; i < 3000; i++ {
		b.WriteString("line\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}

	tool := NewFSTool(dir)
	in, _ := json.Marshal(FSInput{
		Action: "read",
		Path:   "small.txt",
		Offset: 1,
		Limit:  999999,
	})
	raw, err := tool.Execute(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	var res ToolResult
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatal(err)
	}
	count := strings.Count(res.Output, "\n")
	if count > rangedReadLineCap+5 {
		t.Errorf("ranged-read returned %d lines, want <= %d", count, rangedReadLineCap)
	}
}

// TestRead_SmallFileFullRead: confirm we DIDN'T regress the happy path
// — a normal-sized file still returns its full contents.
func TestRead_SmallFileFullRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "small.go")
	if err := os.WriteFile(path, []byte("package x\n\nfunc Y() int { return 1 }\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	tool := NewFSTool(dir)
	in, _ := json.Marshal(FSInput{Action: "read", Path: "small.go"})
	raw, err := tool.Execute(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	var res ToolResult
	_ = json.Unmarshal(raw, &res)
	if !strings.Contains(res.Output, "func Y()") {
		t.Errorf("small file should round-trip its content: %q", res.Output)
	}
	if strings.Contains(res.Output, "FILE TOO LARGE") {
		t.Error("small file should not trigger large-file path")
	}
}

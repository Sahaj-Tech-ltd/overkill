package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestHeadTailCompressor_ShortOutput_Untouched(t *testing.T) {
	c := NewHeadTailCompressor("fs", 1024, 512)
	in, _ := json.Marshal(ToolResult{Output: "hello world", Success: true})
	out, saved, err := c.Compress(in)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if saved != 0 {
		t.Fatalf("expected no savings on short output, got %d", saved)
	}
	if string(out) != string(in) {
		t.Fatalf("output mutated unexpectedly")
	}
}

func TestHeadTailCompressor_LongOutput_Trimmed(t *testing.T) {
	body := strings.Repeat("the quick brown fox jumps over the lazy dog\n", 1000)
	c := NewHeadTailCompressor("fs", 1024, 512)
	in, _ := json.Marshal(ToolResult{Output: body, Success: true})
	out, saved, err := c.Compress(in)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if saved <= 0 {
		t.Fatalf("expected savings, got %d", saved)
	}
	var got ToolResult
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !strings.Contains(got.Output, "[truncated") {
		t.Fatalf("missing truncation marker: %q", got.Output)
	}
	if len(got.Output) >= len(body) {
		t.Fatalf("output not actually shorter: %d vs %d", len(got.Output), len(body))
	}
}

func TestHeadTailCompressor_GenericContentField(t *testing.T) {
	body := strings.Repeat("xxxx\n", 5000)
	in, _ := json.Marshal(map[string]any{"content": body, "url": "https://x"})
	c := NewHeadTailCompressor("web", 2048, 1024)
	out, saved, err := c.Compress(in)
	if err != nil || saved <= 0 {
		t.Fatalf("expected savings, got saved=%d err=%v", saved, err)
	}
	if !strings.Contains(string(out), "truncated") {
		t.Fatalf("missing truncation: %s", out)
	}
}

func TestPatchCompressor_DropsLargeDiff(t *testing.T) {
	in, _ := json.Marshal(map[string]any{
		"applied": true,
		"result":  strings.Repeat("+ line\\n", 1000),
	})
	out, saved, err := PatchCompressor{}.Compress(in)
	if err != nil || saved <= 0 {
		t.Fatalf("expected savings, got %d err=%v", saved, err)
	}
	if !strings.Contains(string(out), "[truncated") {
		t.Fatalf("expected truncation marker: %s", out)
	}
}

func TestPatchCompressor_KeepsErrorOutputs(t *testing.T) {
	body := strings.Repeat("x", 5000)
	in, _ := json.Marshal(map[string]any{"error": "permission denied", "diff": body})
	out, saved, err := PatchCompressor{}.Compress(in)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if saved != 0 {
		t.Fatalf("error path should not be compressed (need debugging context), saved=%d out=%s", saved, out)
	}
}

func TestRegistry_DispatchesNewCompressors(t *testing.T) {
	cr := NewCompressorRegistry()
	body := strings.Repeat("foo\n", 5000)
	in, _ := json.Marshal(ToolResult{Output: body})
	for _, tool := range []string{"fs", "web", "lsp_hover", "browser_markdown"} {
		out, saved, err := cr.Compress(tool, in)
		if err != nil || saved <= 0 {
			t.Errorf("%s: expected savings, got %d err=%v", tool, saved, err)
		}
		if string(out) == string(in) {
			t.Errorf("%s: output unchanged", tool)
		}
	}
}

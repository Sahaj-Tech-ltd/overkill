package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/multimodal"
)

func TestUnderstand_RequiresPath(t *testing.T) {
	tool := NewUnderstandTool(multimodal.DefaultRegistry(nil), "")
	_, err := tool.Execute(context.Background(), []byte(`{}`))
	if err == nil {
		t.Error("missing path should error")
	}
}

func TestUnderstand_ReadsTextFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	_ = os.WriteFile(path, []byte("hello multimodal world"), 0o644)

	tool := NewUnderstandTool(multimodal.DefaultRegistry(nil), dir)
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"note.txt"}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var got map[string]any
	_ = json.Unmarshal(out, &got)
	text, _ := got["text"].(string)
	if !strings.Contains(text, "hello multimodal world") {
		t.Errorf("text missing content: %q", text)
	}
	if got["extractor"] != "text" {
		t.Errorf("extractor: %v", got["extractor"])
	}
}

func TestUnderstand_ResolvesRelativeAgainstCwd(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "docs")
	_ = os.MkdirAll(sub, 0o755)
	path := filepath.Join(sub, "spec.md")
	_ = os.WriteFile(path, []byte("# Spec\n\nbody"), 0o644)

	tool := NewUnderstandTool(multimodal.DefaultRegistry(nil), dir)
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"docs/spec.md"}`))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	_ = json.Unmarshal(out, &got)
	if !strings.Contains(got["text"].(string), "Spec") {
		t.Errorf("relative path not resolved: %v", got)
	}
}

func TestUnderstand_UnknownBinaryFallsBackGracefully(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "weird.xyz")
	_ = os.WriteFile(path, []byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05}, 0o644)

	tool := NewUnderstandTool(multimodal.DefaultRegistry(nil), dir)
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"weird.xyz"}`))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	_ = json.Unmarshal(out, &got)
	// Either the text extractor (octet-stream sniff might look textable)
	// or the binary fallback — both produce non-empty text. The
	// contract is "no error", not a specific extractor.
	if got["text"] == "" {
		t.Errorf("unknown binary should still produce text: %v", got)
	}
}

func TestUnderstand_MissingFileErrors(t *testing.T) {
	tool := NewUnderstandTool(multimodal.DefaultRegistry(nil), "")
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"/totally/missing.xyz"}`))
	if err == nil {
		t.Error("missing file should error")
	}
}

func TestUnderstand_NoRegistryErrors(t *testing.T) {
	tool := &UnderstandTool{Registry: nil}
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"path":"x"}`))
	if err == nil {
		t.Error("missing registry should error")
	}
}

func TestUnderstand_Name(t *testing.T) {
	tool := NewUnderstandTool(nil, "")
	if tool.Name() != "understand_anything" {
		t.Errorf("name: %s", tool.Name())
	}
}

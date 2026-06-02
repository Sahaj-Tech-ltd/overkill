package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// rootResolver returns a fixed path — test helper so the lazy
// resolver still works without mocking os.Getwd.
func rootResolver(p string) ProjectRootResolver {
	return func() string { return p }
}

func TestArchReadTool_MissingFile(t *testing.T) {
	dir := t.TempDir()
	tool := NewArchReadTool(rootResolver(dir))
	got, _ := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if !strings.Contains(string(got), "not yet generated") {
		t.Errorf("expected not-yet-generated error, got %s", got)
	}
}

func TestArchReadTool_ReadsContent(t *testing.T) {
	dir := t.TempDir()
	content := "# Architecture\n\n## Layers\n- edge\n"
	if err := os.WriteFile(filepath.Join(dir, "OVERKILL_ARCH.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := NewArchReadTool(rootResolver(dir))
	raw, _ := tool.Execute(context.Background(), json.RawMessage(`{}`))
	var out struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.Content != content {
		t.Errorf("content mismatch: %q", out.Content)
	}
}

func TestGlossaryAddTerm_NewEntry(t *testing.T) {
	dir := t.TempDir()
	// Seed with an empty glossary so the append path runs.
	if err := os.WriteFile(filepath.Join(dir, "CONTEXT.md"), []byte("# Glossary\n\n## Terms\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := NewGlossaryAddTermTool(rootResolver(dir))
	raw, err := tool.Execute(context.Background(), json.RawMessage(
		`{"term":"trace-id","definition":"a unique correlator across distributed components","example":"each gRPC request carries a trace-id header"}`))
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Term     string `json:"term"`
		Replaced bool   `json:"replaced"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.Replaced {
		t.Error("first add should not report replaced=true")
	}
	body, _ := os.ReadFile(filepath.Join(dir, "CONTEXT.md"))
	if !strings.Contains(string(body), "### `trace-id`") {
		t.Errorf("term not appended: %s", body)
	}
	if !strings.Contains(string(body), "unique correlator") {
		t.Errorf("definition not written: %s", body)
	}
}

func TestGlossaryAddTerm_ReplacesExisting(t *testing.T) {
	dir := t.TempDir()
	seed := "# Glossary\n\n### `trace-id`\n\nold definition\n"
	if err := os.WriteFile(filepath.Join(dir, "CONTEXT.md"), []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := NewGlossaryAddTermTool(rootResolver(dir))
	raw, _ := tool.Execute(context.Background(), json.RawMessage(
		`{"term":"trace-id","definition":"NEW DEFINITION"}`))
	var out struct {
		Replaced bool `json:"replaced"`
	}
	_ = json.Unmarshal(raw, &out)
	if !out.Replaced {
		t.Error("expected replaced=true on existing term")
	}
	body, _ := os.ReadFile(filepath.Join(dir, "CONTEXT.md"))
	bodyStr := string(body)
	if !strings.Contains(bodyStr, "NEW DEFINITION") {
		t.Error("definition should be overwritten")
	}
	if strings.Contains(bodyStr, "old definition") {
		t.Error("old definition should be gone after replace")
	}
}

func TestGlossaryAddTerm_RequiresFields(t *testing.T) {
	dir := t.TempDir()
	tool := NewGlossaryAddTermTool(rootResolver(dir))
	got, _ := tool.Execute(context.Background(), json.RawMessage(`{}`))
	if !strings.Contains(string(got), "term is required") {
		t.Errorf("expected term-required, got %s", got)
	}
	got, _ = tool.Execute(context.Background(), json.RawMessage(`{"term":"x"}`))
	if !strings.Contains(string(got), "definition is required") {
		t.Errorf("expected definition-required, got %s", got)
	}
}

func TestGlossaryAddTerm_SluggedTerm(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "CONTEXT.md"), []byte("# Glossary\n\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := NewGlossaryAddTermTool(rootResolver(dir))
	_, _ = tool.Execute(context.Background(), json.RawMessage(
		`{"term":"Trace ID","definition":"x"}`))
	body, _ := os.ReadFile(filepath.Join(dir, "CONTEXT.md"))
	if !strings.Contains(string(body), "### `trace-id`") {
		t.Errorf("term should be slugged lowercase + hyphenated, got: %s", body)
	}
}

package pipeline

import (
	"strings"
	"testing"
)

func TestRenderPlan(t *testing.T) {
	markdown := []byte(`# Task Manager API

A RESTful API for managing personal tasks.

## Requirements

1. Users must create tasks with **title** and description
2. Tasks support statuses: *pending*, *in-progress*, *done*
3. Uses ` + "`JWT`" + ` auth

## API Surface

` + "`POST /api/tasks`" + ` — Create task

### Request

` + "```json\n{\n  \"title\": \"Buy groceries\"\n}\n```" + `

## Architecture

- Frontend: React
- Backend: Go

Files:
- internal/api/handlers.go
- internal/model/task.go

## Edge Cases

- Empty title → 400
- Invalid JWT → 401
`)

	html := RenderPlan(markdown, RenderConfig{Name: "task-manager"})

	// Basic structure checks.
	if !strings.HasPrefix(html, "<!DOCTYPE html>") {
		t.Error("missing DOCTYPE")
	}
	if !strings.Contains(html, "<html lang=") {
		t.Error("missing html tag")
	}
	if !strings.Contains(html, "</html>") {
		t.Error("missing closing html tag")
	}

	// Title.
	if !strings.Contains(html, "Task Manager API") {
		t.Error("title not rendered")
	}

	// TOC.
	if !strings.Contains(html, "toc-list") {
		t.Error("TOC missing")
	}
	if !strings.Contains(html, `href="#requirements"`) {
		t.Error("TOC link to requirements missing")
	}
	if !strings.Contains(html, `href="#api-surface"`) {
		t.Error("TOC link to api-surface missing")
	}

	// Code blocks with syntax highlighting.
	if !strings.Contains(html, "code-block") {
		t.Error("code block missing")
	}

	// File tree.
	if !strings.Contains(html, "handlers.go") {
		t.Error("file tree missing handlers.go")
	}
	if !strings.Contains(html, "task.go") {
		t.Error("file tree missing task.go")
	}

	// Dark theme.
	if !strings.Contains(html, "#1a1a2e") {
		t.Error("dark theme bg color missing")
	}
	if !strings.Contains(html, "#e94560") {
		t.Error("accent color missing")
	}

	// Architecture section.
	if !strings.Contains(html, "Architecture Overview") {
		t.Error("architecture diagram placeholder missing")
	}

	// Collapsible section (should wrap details).
	if !strings.Contains(html, "Mobile") {
		t.Error("responsive/mobile CSS missing")
	}

	// Bold/italic inline rendering.
	if !strings.Contains(html, "<strong>") {
		t.Error("bold formatting not rendered")
	}
	if !strings.Contains(html, "<em>") {
		t.Error("italic formatting not rendered")
	}

	// Inline code.
	if !strings.Contains(html, "code class=\"inline\"") {
		t.Error("inline code not rendered")
	}

	t.Logf("HTML output: %d bytes", len(html))
}

func TestRenderPlan_Empty(t *testing.T) {
	html := RenderPlan([]byte(""), RenderConfig{})
	if !strings.Contains(html, "Plan") {
		t.Error("empty plan should have default title")
	}
	if !strings.HasPrefix(html, "<!DOCTYPE html>") {
		t.Error("should produce valid HTML even for empty input")
	}
}

func TestRenderPlan_NoCodeBlocks(t *testing.T) {
	markdown := []byte(`# Simple Plan

Just a paragraph with no code.

- Item one
- Item two
`)
	html := RenderPlan(markdown, RenderConfig{})
	if !strings.Contains(html, "Simple Plan") {
		t.Error("title missing")
	}
	if !strings.Contains(html, "Item one") {
		t.Error("list items missing")
	}
	// Should not have broken code block refs.
	if strings.Contains(html, "%%CODEBLOCK") {
		t.Error("stray placeholder found")
	}
}

func TestRenderPlanToFile(t *testing.T) {
	markdown := []byte("# File Test\n\nContent here.\n")
	path, err := RenderPlanToFile(markdown, RenderConfig{Name: "file-test"})
	if err != nil {
		t.Fatalf("RenderPlanToFile: %v", err)
	}
	if path == "" {
		t.Error("path should not be empty")
	}
	if !strings.HasSuffix(path, ".html") {
		t.Error("path should end with .html")
	}
	t.Logf("rendered to: %s", path)
}

func TestSlugify(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"Task Manager API", "task-manager-api"},
		{"Hello, World!", "hello-world"},
		{"UPPERCASE", "uppercase"},
		{"  spaces  ", "spaces"},
	}
	for _, tt := range tests {
		got := slugify(tt.input)
		if got != tt.expected {
			t.Errorf("slugify(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestExtractTitle(t *testing.T) {
	tests := []struct {
		content  string
		expected string
	}{
		{"# My Plan\n\nContent", "My Plan"},
		{"No heading here", "No heading here"},
		{"", "Plan"},
	}
	for _, tt := range tests {
		got := extractTitle(tt.content)
		if got != tt.expected {
			t.Errorf("extractTitle(%q) = %q, want %q", tt.content, got, tt.expected)
		}
	}
}

func TestExtractFileTree(t *testing.T) {
	content := "Files:\n- `internal/api/handlers.go`\n- `internal/model/task.go`\n- `cmd/server/main.go`\n"
	tree := extractFileTree(content)
	if len(tree) == 0 {
		t.Fatal("expected file tree entries")
	}

	found := make(map[string]bool)
	var walk func(entries []fileTreeEntry)
	walk = func(entries []fileTreeEntry) {
		for _, e := range entries {
			found[e.Name] = true
			walk(e.Children)
		}
	}
	walk(tree)

	if !found["handlers.go"] {
		t.Error("handlers.go not in tree")
	}
	if !found["task.go"] {
		t.Error("task.go not in tree")
	}
}

func TestExtractTOC(t *testing.T) {
	content := `# Title

## Requirements

### Sub-req

## API`
	toc := extractTOC(content)
	if len(toc) < 3 {
		t.Fatalf("expected at least 3 TOC entries, got %d", len(toc))
	}
	if toc[0].Level != 1 {
		t.Errorf("first entry level = %d, want 1", toc[0].Level)
	}
	if toc[1].Level != 2 {
		t.Errorf("second entry level = %d, want 2", toc[1].Level)
	}
	if toc[2].Level != 3 {
		t.Errorf("third entry level = %d, want 3", toc[2].Level)
	}
}

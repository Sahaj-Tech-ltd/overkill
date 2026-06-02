package automemory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewExtractor(t *testing.T) {
	e := NewExtractor("/tmp")
	if e.HomeDir != "/tmp" {
		t.Errorf("HomeDir: got %q, want /tmp", e.HomeDir)
	}
	if e.MinNewMessages != 5 {
		t.Errorf("MinNewMessages: got %d, want 5", e.MinNewMessages)
	}
}

func TestShouldExtract_NoExtractFn(t *testing.T) {
	e := NewExtractor("/tmp")
	if e.ShouldExtract(10, "msg-123") {
		t.Error("ShouldExtract should be false when ExtractFn is nil")
	}
}

func TestShouldExtract_BelowThreshold(t *testing.T) {
	e := NewExtractor("/tmp")
	e.ExtractFn = func(_ context.Context, _ string) ([]Fact, error) { return nil, nil }

	if e.ShouldExtract(3, "msg-123") {
		t.Error("ShouldExtract should be false below MinNewMessages")
	}
}

func TestShouldExtract_MeetsThreshold(t *testing.T) {
	e := NewExtractor("/tmp")
	e.ExtractFn = func(_ context.Context, _ string) ([]Fact, error) { return nil, nil }

	if !e.ShouldExtract(5, "msg-123") {
		t.Error("ShouldExtract should be true when threshold met")
	}
}

func TestMarkExtracted(t *testing.T) {
	e := NewExtractor("/tmp")
	e.MarkExtracted("msg-456")

	e.mu.Lock()
	if e.lastExtractedAt != "msg-456" {
		t.Errorf("lastExtractedAt: got %q, want msg-456", e.lastExtractedAt)
	}
	e.mu.Unlock()
}

func TestExtract_NoExtractFn(t *testing.T) {
	e := NewExtractor("/tmp")
	err := e.Extract(context.Background(), "transcript")
	if err == nil || !strings.Contains(err.Error(), "ExtractFn not set") {
		t.Errorf("expected ExtractFn not set error, got: %v", err)
	}
}

func TestExtract_WritesFacts(t *testing.T) {
	dir := t.TempDir()
	e := NewExtractor(dir)
	e.ExtractFn = func(_ context.Context, _ string) ([]Fact, error) {
		return []Fact{
			{Content: "User prefers concise responses", Category: "user"},
			{Content: "Project uses Go + TypeScript", Category: "project"},
		}, nil
	}

	if err := e.Extract(context.Background(), "some transcript"); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	// Check file was written.
	memDir := filepath.Join(dir, ".overkill", "memory")
	entries, err := os.ReadDir(memDir)
	if err != nil {
		t.Fatalf("ReadDir memory dir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no memory files written")
	}

	data, err := os.ReadFile(filepath.Join(memDir, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, "User prefers concise responses") {
		t.Errorf("memory file missing user fact: %s", content)
	}
	if !strings.Contains(content, "Project uses Go + TypeScript") {
		t.Errorf("memory file missing project fact: %s", content)
	}
}

func TestExtract_EmptyFacts(t *testing.T) {
	dir := t.TempDir()
	e := NewExtractor(dir)
	e.ExtractFn = func(_ context.Context, _ string) ([]Fact, error) {
		return nil, nil
	}

	// Should not error and should not create memory dir.
	err := e.Extract(context.Background(), "transcript")
	if err != nil {
		t.Fatalf("Extract with empty facts: %v", err)
	}

	memDir := filepath.Join(dir, ".overkill", "memory")
	if _, err := os.Stat(memDir); !os.IsNotExist(err) {
		t.Error("memory dir should not exist for empty facts")
	}
}

func TestFormatFact(t *testing.T) {
	fact := Fact{Content: "hello", Category: "general"}
	got := formatFact(fact)
	if got != "- [general] hello" {
		t.Errorf("formatFact: got %q", got)
	}
}

func TestIsDuplicate(t *testing.T) {
	existing := []string{"- [user] User likes Go", "- [project] Uses Postgres"}

	if !isDuplicate(existing, "User likes Go") {
		t.Error("should detect duplicate (case-insensitive)")
	}
	if isDuplicate(existing, "New information") {
		t.Error("should not match unrelated content")
	}
	if isDuplicate(existing, "") {
		t.Error("empty content should never match")
	}
}

func TestReadMemoryFile_Nonexistent(t *testing.T) {
	lines := readMemoryFile("/tmp/automemory-test-nonexistent-file")
	if lines != nil {
		t.Errorf("expected nil for nonexistent file, got %v", lines)
	}
}

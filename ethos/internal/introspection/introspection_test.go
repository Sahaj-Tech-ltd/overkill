package introspection

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/ethos/internal/providers"
)

func mockHandler(content string) func(req providers.Request) (providers.Response, error) {
	return func(req providers.Request) (providers.Response, error) {
		return providers.Response{
			ID:      "test-id",
			Model:   "test-model",
			Content: content,
			Usage:   providers.Usage{InputTokens: 10, OutputTokens: 20},
		}, nil
	}
}

func mockErrorHandler(err error) func(req providers.Request) (providers.Response, error) {
	return func(req providers.Request) (providers.Response, error) {
		return providers.Response{}, err
	}
}

func newTestIntrospector(t *testing.T, handler func(providers.Request) (providers.Response, error)) (*Introspector, string) {
	t.Helper()
	dir := t.TempDir()
	provider := providers.NewMockProvider("test", nil, handler)
	return NewIntrospector(dir, provider, "test-model"), dir
}

func TestIntrospector_Get_Existing(t *testing.T) {
	dir := t.TempDir()
	provider := providers.NewMockProvider("test", nil, mockHandler(""))
	intro := NewIntrospector(dir, provider, "test-model")

	content := "# Codebase Map\n\nSome content here."
	path := filepath.Join(dir, string(FileCodebase))
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("setup: write file: %v", err)
	}

	f, err := intro.Get(FileCodebase)
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}

	if !f.Exists {
		t.Fatal("Get() Exists = false, want true")
	}
	if f.Content != content {
		t.Errorf("Get() Content = %q, want %q", f.Content, content)
	}
	if f.Path != path {
		t.Errorf("Get() Path = %q, want %q", f.Path, path)
	}
	if f.Type != FileCodebase {
		t.Errorf("Get() Type = %q, want %q", f.Type, FileCodebase)
	}
	if f.UpdatedAt.IsZero() {
		t.Error("Get() UpdatedAt is zero")
	}
}

func TestIntrospector_Get_Missing(t *testing.T) {
	intro, _ := newTestIntrospector(t, mockHandler(""))

	f, err := intro.Get(FileCodebase)
	if err != nil {
		t.Fatalf("Get() error: %v", err)
	}

	if f.Exists {
		t.Fatal("Get() Exists = true, want false")
	}
	if f.Content != "" {
		t.Errorf("Get() Content = %q, want empty", f.Content)
	}
	if f.Type != FileCodebase {
		t.Errorf("Get() Type = %q, want %q", f.Type, FileCodebase)
	}
}

func TestIntrospector_Generate(t *testing.T) {
	expected := "# Generated Codebase\n\n## Packages\n- foo\n- bar"
	intro, dir := newTestIntrospector(t, mockHandler(expected))

	f, err := intro.Generate(context.Background(), FileCodebase)
	if err != nil {
		t.Fatalf("Generate() error: %v", err)
	}

	if !f.Exists {
		t.Fatal("Generate() Exists = false, want true")
	}
	if f.Content != expected {
		t.Errorf("Generate() Content = %q, want %q", f.Content, expected)
	}
	if f.Type != FileCodebase {
		t.Errorf("Generate() Type = %q, want %q", f.Type, FileCodebase)
	}

	data, err := os.ReadFile(filepath.Join(dir, string(FileCodebase)))
	if err != nil {
		t.Fatalf("read generated file: %v", err)
	}
	if string(data) != expected {
		t.Errorf("file content = %q, want %q", string(data), expected)
	}
}

func TestIntrospector_GenerateAll(t *testing.T) {
	intro, dir := newTestIntrospector(t, mockHandler("generated content"))

	files, err := intro.GenerateAll(context.Background())
	if err != nil {
		t.Fatalf("GenerateAll() error: %v", err)
	}

	if len(files) != 4 {
		t.Fatalf("GenerateAll() returned %d files, want 4", len(files))
	}

	expected := []FileType{FileCodebase, FileModelCard, FileKnownIssues, FileArchitecture}
	for i, ft := range expected {
		if files[i].Type != ft {
			t.Errorf("files[%d].Type = %q, want %q", i, files[i].Type, ft)
		}
		if !files[i].Exists {
			t.Errorf("files[%d].Exists = false, want true", i)
		}
		path := filepath.Join(dir, string(ft))
		if _, err := os.Stat(path); err != nil {
			t.Errorf("file %s not on disk: %v", ft, err)
		}
	}
}

func TestIntrospector_IsStale_Fresh(t *testing.T) {
	intro, dir := newTestIntrospector(t, mockHandler(""))

	path := filepath.Join(dir, string(FileCodebase))
	if err := os.WriteFile(path, []byte("content"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if intro.IsStale(FileCodebase, time.Hour) {
		t.Error("IsStale() = true for fresh file, want false")
	}
}

func TestIntrospector_IsStale_Stale(t *testing.T) {
	intro, dir := newTestIntrospector(t, mockHandler(""))

	path := filepath.Join(dir, string(FileCodebase))
	if err := os.WriteFile(path, []byte("content"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	staleTime := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(path, staleTime, staleTime); err != nil {
		t.Fatalf("setup: chtimes: %v", err)
	}

	if !intro.IsStale(FileCodebase, time.Hour) {
		t.Error("IsStale() = false for stale file, want true")
	}
}

func TestIntrospector_IsStale_Missing(t *testing.T) {
	intro, _ := newTestIntrospector(t, mockHandler(""))

	if !intro.IsStale(FileCodebase, time.Hour) {
		t.Error("IsStale() = false for missing file, want true")
	}
}

func TestIntrospector_List(t *testing.T) {
	intro, dir := newTestIntrospector(t, mockHandler(""))

	codebasePath := filepath.Join(dir, string(FileCodebase))
	if err := os.WriteFile(codebasePath, []byte("codebase content"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}

	files, err := intro.List()
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}

	if len(files) != 4 {
		t.Fatalf("List() returned %d files, want 4", len(files))
	}

	existingCount := 0
	for _, f := range files {
		if f.Exists {
			existingCount++
		}
	}
	if existingCount != 1 {
		t.Errorf("found %d existing files, want 1", existingCount)
	}
}

func TestIntrospector_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	expectedErr := errors.New("context canceled")
	intro, _ := newTestIntrospector(t, mockErrorHandler(expectedErr))

	_, err := intro.Generate(ctx, FileCodebase)
	if err == nil {
		t.Fatal("Generate() expected error, got nil")
	}
}

func TestGenerateCodebase(t *testing.T) {
	content := "# Codebase\n\n## Structure\n```\ncmd/\ninternal/\n```"
	dir := t.TempDir()
	provider := providers.NewMockProvider("test", nil, mockHandler(content))

	f, err := generateCodebase(context.Background(), provider, "test-model", dir)
	if err != nil {
		t.Fatalf("generateCodebase() error: %v", err)
	}

	if f.Type != FileCodebase {
		t.Errorf("Type = %q, want %q", f.Type, FileCodebase)
	}
	if f.Content != content {
		t.Errorf("Content = %q, want %q", f.Content, content)
	}
	if !f.Exists {
		t.Error("Exists = false, want true")
	}

	data, err := os.ReadFile(filepath.Join(dir, string(FileCodebase)))
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(data) != content {
		t.Errorf("file = %q, want %q", string(data), content)
	}
}

func TestGenerateModelCard(t *testing.T) {
	content := "# Model Card\n\n| Model | Context | Price |"
	dir := t.TempDir()
	provider := providers.NewMockProvider("test", nil, mockHandler(content))

	f, err := generateModelCard(context.Background(), provider, "test-model", dir)
	if err != nil {
		t.Fatalf("generateModelCard() error: %v", err)
	}

	if f.Type != FileModelCard {
		t.Errorf("Type = %q, want %q", f.Type, FileModelCard)
	}
	if f.Content != content {
		t.Errorf("Content = %q, want %q", f.Content, content)
	}

	data, err := os.ReadFile(filepath.Join(dir, string(FileModelCard)))
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(data) != content {
		t.Errorf("file = %q, want %q", string(data), content)
	}
}

func TestGenerateKnownIssues(t *testing.T) {
	content := "# Known Issues\n\n- [ ] Issue 1\n- [ ] Issue 2"
	dir := t.TempDir()
	provider := providers.NewMockProvider("test", nil, mockHandler(content))

	f, err := generateKnownIssues(context.Background(), provider, "test-model", dir)
	if err != nil {
		t.Fatalf("generateKnownIssues() error: %v", err)
	}

	if f.Type != FileKnownIssues {
		t.Errorf("Type = %q, want %q", f.Type, FileKnownIssues)
	}
	if f.Content != content {
		t.Errorf("Content = %q, want %q", f.Content, content)
	}

	data, err := os.ReadFile(filepath.Join(dir, string(FileKnownIssues)))
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(data) != content {
		t.Errorf("file = %q, want %q", string(data), content)
	}
}

func TestGenerateArchitecture(t *testing.T) {
	content := "# Architecture\n\n## ADR-001: Use BadgerDB"
	dir := t.TempDir()
	provider := providers.NewMockProvider("test", nil, mockHandler(content))

	f, err := generateArchitecture(context.Background(), provider, "test-model", dir)
	if err != nil {
		t.Fatalf("generateArchitecture() error: %v", err)
	}

	if f.Type != FileArchitecture {
		t.Errorf("Type = %q, want %q", f.Type, FileArchitecture)
	}
	if f.Content != content {
		t.Errorf("Content = %q, want %q", f.Content, content)
	}

	data, err := os.ReadFile(filepath.Join(dir, string(FileArchitecture)))
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(data) != content {
		t.Errorf("file = %q, want %q", string(data), content)
	}
}

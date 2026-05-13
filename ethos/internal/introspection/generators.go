package introspection

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

const (
	systemPromptCodebase     = "Generate a codebase map for a Go project. List: directory structure, key packages, public interfaces, dependencies. Format as markdown with code blocks. Be comprehensive but concise."
	systemPromptModelCard    = "Generate a model card documenting current AI model capabilities, limitations, pricing, and context windows. Format as markdown table."
	systemPromptKnownIssues  = "Generate a known issues document. Common gotchas, workarounds, frequently encountered errors. Format as markdown checklist."
	systemPromptArchitecture = "Generate an architecture decision record. Document key architectural decisions, patterns used, trade-offs made. Format as markdown with ADR-style entries."
)

func generateCodebase(ctx context.Context, provider providers.Provider, model string, dir string) (*IntrospectionFile, error) {
	return generateFile(ctx, provider, model, dir, FileCodebase, systemPromptCodebase)
}

func generateModelCard(ctx context.Context, provider providers.Provider, model string, dir string) (*IntrospectionFile, error) {
	return generateFile(ctx, provider, model, dir, FileModelCard, systemPromptModelCard)
}

func generateKnownIssues(ctx context.Context, provider providers.Provider, model string, dir string) (*IntrospectionFile, error) {
	return generateFile(ctx, provider, model, dir, FileKnownIssues, systemPromptKnownIssues)
}

func generateArchitecture(ctx context.Context, provider providers.Provider, model string, dir string) (*IntrospectionFile, error) {
	return generateFile(ctx, provider, model, dir, FileArchitecture, systemPromptArchitecture)
}

func generateFile(ctx context.Context, provider providers.Provider, model string, dir string, fileType FileType, systemPrompt string) (*IntrospectionFile, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("introspection: create dir: %w", err)
	}

	req := providers.Request{
		Model: model,
		Messages: []providers.Message{
			{Role: "user", Content: string(fileType)},
		},
		SystemPrompt: systemPrompt,
	}

	resp, err := provider.Complete(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("introspection: llm complete: %w", err)
	}

	path := filepath.Join(dir, string(fileType))
	if err := os.WriteFile(path, []byte(resp.Content), 0o644); err != nil {
		return nil, fmt.Errorf("introspection: write %s: %w", fileType, err)
	}

	return &IntrospectionFile{
		Type:      fileType,
		Path:      path,
		Content:   resp.Content,
		UpdatedAt: time.Now(),
		Exists:    true,
	}, nil
}

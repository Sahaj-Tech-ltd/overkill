// orphan: on-demand CODEBASE.md generator (master plan §5.8); needs /introspect slash command
package introspection

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/atomicfile"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

type FileType string

const (
	FileCodebase     FileType = "CODEBASE.md"
	FileModelCard    FileType = "MODEL_CARD.md"
	FileKnownIssues  FileType = "KNOWN_ISSUES.md"
	FileArchitecture FileType = "ARCHITECTURE.md"
)

type IntrospectionFile struct {
	Type      FileType  `json:"type"`
	Path      string    `json:"path"`
	Content   string    `json:"content"`
	UpdatedAt time.Time `json:"updated_at"`
	Exists    bool      `json:"exists"`
}

type Introspector struct {
	dir      string
	provider providers.Provider
	model    string
}

func NewIntrospector(dir string, provider providers.Provider, model string) *Introspector {
	return &Introspector{
		dir:      dir,
		provider: provider,
		model:    model,
	}
}

func (i *Introspector) Get(fileType FileType) (*IntrospectionFile, error) {
	path := filepath.Join(i.dir, string(fileType))
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &IntrospectionFile{
				Type:   fileType,
				Path:   path,
				Exists: false,
			}, nil
		}
		return nil, fmt.Errorf("introspection: read %s: %w", fileType, err)
	}

	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("introspection: stat %s: %w", fileType, err)
	}

	return &IntrospectionFile{
		Type:      fileType,
		Path:      path,
		Content:   string(data),
		UpdatedAt: info.ModTime(),
		Exists:    true,
	}, nil
}

func (i *Introspector) Generate(ctx context.Context, fileType FileType) (*IntrospectionFile, error) {
	var generator func(context.Context, providers.Provider, string, string) (*IntrospectionFile, error)

	switch fileType {
	case FileCodebase:
		generator = generateCodebase
	case FileModelCard:
		generator = generateModelCard
	case FileKnownIssues:
		generator = generateKnownIssues
	case FileArchitecture:
		generator = generateArchitecture
	default:
		return nil, fmt.Errorf("introspection: unknown file type: %s", fileType)
	}

	f, err := generator(ctx, i.provider, i.model, i.dir)
	if err != nil {
		return nil, fmt.Errorf("introspection: generate %s: %w", fileType, err)
	}

	return f, nil
}

func (i *Introspector) GenerateAll(ctx context.Context) ([]IntrospectionFile, error) {
	types := []FileType{FileCodebase, FileModelCard, FileKnownIssues, FileArchitecture}
	results := make([]IntrospectionFile, 0, len(types))

	for _, ft := range types {
		f, err := i.Generate(ctx, ft)
		if err != nil {
			return nil, fmt.Errorf("introspection: generate all (%s): %w", ft, err)
		}
		results = append(results, *f)
	}

	return results, nil
}

func (i *Introspector) IsStale(fileType FileType, maxAge time.Duration) bool {
	f, err := i.Get(fileType)
	if err != nil || !f.Exists {
		return true
	}

	return time.Since(f.UpdatedAt) > maxAge
}

// WriteCodebaseFromScan walks the given source directory deterministically,
// renders a CODEBASE.md, and writes it to the introspector's directory.
// Returns the resulting file. Used by /init to seed the deep-wiki without an
// LLM call.
func WriteCodebaseFromScan(sourceDir, outDir string) (*IntrospectionFile, error) {
	res, err := WalkAndSummarize(sourceDir)
	if err != nil {
		return nil, fmt.Errorf("introspection: scan: %w", err)
	}
	body := RenderCodebaseMarkdown(res)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, fmt.Errorf("introspection: mkdir: %w", err)
	}
	path := filepath.Join(outDir, string(FileCodebase))
	if err := atomicfile.WriteFile(path, []byte(body), 0o644); err != nil {
		return nil, fmt.Errorf("introspection: write: %w", err)
	}
	return &IntrospectionFile{
		Type:      FileCodebase,
		Path:      path,
		Content:   body,
		UpdatedAt: time.Now(),
		Exists:    true,
	}, nil
}

// LoadCodebaseSnippet returns a project-context snippet suitable for injection
// into the agent's system prompt. Returns "" if no CODEBASE.md exists at dir.
// Truncates to maxChars to keep the system prompt budget under control.
func LoadCodebaseSnippet(dir string, maxChars int) string {
	if maxChars <= 0 {
		maxChars = 8000
	}
	path := filepath.Join(dir, string(FileCodebase))
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	body := string(data)
	if len(body) > maxChars {
		body = body[:maxChars] + "\n\n[truncated]"
	}
	return "## Project context (from CODEBASE.md)\n\n" + body
}

func (i *Introspector) List() ([]IntrospectionFile, error) {
	types := []FileType{FileCodebase, FileModelCard, FileKnownIssues, FileArchitecture}
	results := make([]IntrospectionFile, 0, len(types))

	for _, ft := range types {
		f, err := i.Get(ft)
		if err != nil {
			return nil, fmt.Errorf("introspection: list: %w", err)
		}
		results = append(results, *f)
	}

	return results, nil
}

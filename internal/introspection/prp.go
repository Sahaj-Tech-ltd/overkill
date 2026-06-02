// Package introspection — PRP (Project Requirements Prompt) generator
// (master plan §4.11).
//
// PRP.md is a one-page briefing emitted alongside CODEBASE.md. It captures
// the project's *purpose*, not just its file layout: what the project is,
// who uses it, what the current goals are, and what conventions to honor.
//
// We emit a structured stub with sections the agent (or the user) fills
// in once. Once written, the stub is preserved across regenerations —
// users hand-edit PRP.md and we do not clobber it.
package introspection

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/atomicfile"
)

// PRPInputs is what the caller knows at generation time.
type PRPInputs struct {
	ProjectName string
	RepoRoot    string
	Languages   []string // detected via simple file-extension scan
	OutputDir   string   // typically ~/.overkill/introspection
}

// PRPResult reports what was written.
type PRPResult struct {
	Path    string `json:"path"`
	Created bool   `json:"created"` // false → existing file preserved
}

// WritePRP emits a PRP.md scaffold under OutputDir. Existing files are
// preserved (Created=false) so user-curated PRPs survive regeneration.
func WritePRP(in PRPInputs) (*PRPResult, error) {
	if in.OutputDir == "" {
		return nil, fmt.Errorf("introspection: PRP output_dir required")
	}
	if err := os.MkdirAll(in.OutputDir, 0o750); err != nil {
		return nil, err
	}
	out := filepath.Join(in.OutputDir, "PRP.md")
	if _, err := os.Stat(out); err == nil {
		return &PRPResult{Path: out, Created: false}, nil
	}
	body := renderPRP(in)
	if err := atomicfile.WriteFile(out, []byte(body), 0o600); err != nil {
		return nil, err
	}
	return &PRPResult{Path: out, Created: true}, nil
}

func renderPRP(in PRPInputs) string {
	if in.ProjectName == "" {
		in.ProjectName = filepath.Base(in.RepoRoot)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Project Requirements Prompt — %s\n\n", in.ProjectName)
	fmt.Fprintf(&b, "_Generated %s. Hand-edit freely; overkill preserves your changes across regenerations._\n\n",
		time.Now().UTC().Format(time.RFC3339))

	fmt.Fprintf(&b, "## Purpose\n\n_What does this project exist to do? Who is it for?_\n\n")
	fmt.Fprintf(&b, "(fill in)\n\n")

	fmt.Fprintf(&b, "## Stack\n\n")
	if len(in.Languages) > 0 {
		fmt.Fprintf(&b, "Detected languages: %s\n\n", strings.Join(in.Languages, ", "))
	} else {
		fmt.Fprintf(&b, "_No languages auto-detected._\n\n")
	}

	fmt.Fprintf(&b, "## Active goals\n\n_What are we currently building? What's blocked?_\n\n")
	fmt.Fprintf(&b, "- (fill in)\n\n")

	fmt.Fprintf(&b, "## Conventions to honor\n\n")
	fmt.Fprintf(&b, "_Coding style, commit format, branch naming, review process, anything an agent should mirror._\n\n")
	fmt.Fprintf(&b, "- (fill in)\n\n")

	fmt.Fprintf(&b, "## Key files / entry points\n\n")
	fmt.Fprintf(&b, "_The 5-10 files an agent should read first to orient itself._\n\n")
	fmt.Fprintf(&b, "- (fill in)\n\n")

	fmt.Fprintf(&b, "## External dependencies / risks\n\n")
	fmt.Fprintf(&b, "_Third-party services, contracts, security boundaries._\n\n")
	fmt.Fprintf(&b, "- (fill in)\n\n")

	fmt.Fprintf(&b, "## Out of scope\n\n")
	fmt.Fprintf(&b, "_What should an agent NOT touch / suggest / refactor?_\n\n")
	fmt.Fprintf(&b, "- (fill in)\n\n")

	return b.String()
}

// LoadPRPSnippet reads PRP.md and returns up to maxChars. Returns "" when
// the file is missing or unreadable. Used by the agent's per-turn context
// provider to mix the project briefing into every system prompt.
func LoadPRPSnippet(dir string, maxChars int) string {
	if dir == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(dir, "PRP.md"))
	if err != nil {
		return ""
	}
	if maxChars > 0 && len(data) > maxChars {
		return string(data[:maxChars]) + "\n\n... [truncated]"
	}
	return string(data)
}

// DetectLanguages returns a small set of language labels based on file
// extensions present under root. Cheap heuristic; only walks one level deep.
func DetectLanguages(root string) []string {
	want := map[string]string{
		".go":    "Go",
		".py":    "Python",
		".ts":    "TypeScript",
		".tsx":   "TypeScript",
		".js":    "JavaScript",
		".rs":    "Rust",
		".java":  "Java",
		".kt":    "Kotlin",
		".rb":    "Ruby",
		".php":   "PHP",
		".swift": "Swift",
	}
	seen := map[string]bool{}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := filepath.Ext(e.Name())
		if lang, ok := want[ext]; ok {
			seen[lang] = true
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	return out
}

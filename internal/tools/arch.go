// Package tools — arch_read / glossary_read / glossary_add_term
// surface the project's OVERKILL_ARCH.md and CONTEXT.md to the agent
// (master plan §6.5 Wall 2).
//
// The agent reads these for architecture context before non-trivial
// changes, and writes to the glossary when a new domain term is
// established mid-conversation ("term X actually means Y here").
//
// Both files live in the PROJECT ROOT, not the agent's config dir.
// Wall 2 reads them too; this is the same source of truth.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/atomicfile"
)

// ProjectRootResolver supplies the project root path at tool-call
// time. Lazy because the working directory can change between agent
// boot and the first tool call.
type ProjectRootResolver func() string

// ---- arch_read ----

// ArchReadTool returns OVERKILL_ARCH.md content. The agent calls it
// before non-trivial structural changes.
type ArchReadTool struct {
	resolver ProjectRootResolver
}

func NewArchReadTool(r ProjectRootResolver) *ArchReadTool {
	return &ArchReadTool{resolver: r}
}

func (t *ArchReadTool) Name() string { return "arch_read" }

func (t *ArchReadTool) Execute(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
	path := t.archPath()
	if path == "" {
		return errorJSON("project root resolver returned empty path"), nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return errorJSON("OVERKILL_ARCH.md not yet generated — run any boot once to seed it"), nil
		}
		return errorJSON(err.Error()), nil
	}
	body, _ := json.Marshal(map[string]any{
		"path":    path,
		"content": string(data),
	})
	return body, nil
}

func (t *ArchReadTool) archPath() string {
	root := t.resolver()
	if root == "" {
		return ""
	}
	return filepath.Join(root, "OVERKILL_ARCH.md")
}

// ---- glossary_read ----

type GlossaryReadTool struct {
	resolver ProjectRootResolver
}

func NewGlossaryReadTool(r ProjectRootResolver) *GlossaryReadTool {
	return &GlossaryReadTool{resolver: r}
}

func (t *GlossaryReadTool) Name() string { return "glossary_read" }

func (t *GlossaryReadTool) Execute(_ context.Context, _ json.RawMessage) (json.RawMessage, error) {
	root := t.resolver()
	if root == "" {
		return errorJSON("project root resolver returned empty path"), nil
	}
	path := filepath.Join(root, "CONTEXT.md")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return errorJSON("CONTEXT.md not yet generated"), nil
		}
		return errorJSON(err.Error()), nil
	}
	body, _ := json.Marshal(map[string]any{
		"path":    path,
		"content": string(data),
	})
	return body, nil
}

// ---- glossary_add_term ----

// GlossaryAddTermTool appends a new term to CONTEXT.md so the canonical
// vocabulary grows alongside the conversation. Idempotent on the
// term identifier — calling twice with the same `term` overwrites the
// existing entry's body in place.
type GlossaryAddTermTool struct {
	resolver ProjectRootResolver
}

func NewGlossaryAddTermTool(r ProjectRootResolver) *GlossaryAddTermTool {
	return &GlossaryAddTermTool{resolver: r}
}

func (t *GlossaryAddTermTool) Name() string { return "glossary_add_term" }

type glossaryAddInput struct {
	// Term is the canonical identifier — lowercased, hyphenated.
	// Required. Examples: "tracer-bullet-issue", "deletion-test".
	Term string `json:"term"`
	// Definition is the one-line plain-English meaning. Required.
	Definition string `json:"definition"`
	// Example is an optional canonical use case that pins meaning.
	Example string `json:"example,omitempty"`
}

func (t *GlossaryAddTermTool) Execute(_ context.Context, in json.RawMessage) (json.RawMessage, error) {
	var req glossaryAddInput
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, fmt.Errorf("glossary_add_term: %w", err)
	}
	if req.Term == "" {
		return errorJSON("term is required"), nil
	}
	if req.Definition == "" {
		return errorJSON("definition is required"), nil
	}
	term := strings.ToLower(strings.TrimSpace(req.Term))
	term = strings.ReplaceAll(term, " ", "-")

	root := t.resolver()
	if root == "" {
		return errorJSON("project root resolver returned empty path"), nil
	}
	path := filepath.Join(root, "CONTEXT.md")
	existing, _ := os.ReadFile(path)
	updated, replaced := upsertGlossaryTerm(string(existing), term, req.Definition, req.Example)
	if err := atomicfile.WriteFile(path, []byte(updated), 0o644); err != nil {
		return errorJSON(err.Error()), nil
	}
	body, _ := json.Marshal(map[string]any{
		"term":     term,
		"replaced": replaced, // true when an existing entry was overwritten
		"path":     path,
	})
	return body, nil
}

// upsertGlossaryTerm replaces an existing `### `term“ section with
// fresh content if found, otherwise appends a new section at the end.
// Returns (newContent, replaced).
func upsertGlossaryTerm(existing, term, def, example string) (string, bool) {
	header := "### `" + term + "`"
	entry := header + "\n\n" + def + "\n"
	if example != "" {
		entry += "\n_Example: " + example + "_\n"
	}

	idx := strings.Index(existing, header)
	if idx < 0 {
		// New term — append at end with stamped timestamp.
		stamp := "<!-- added " + time.Now().UTC().Format("2006-01-02") + " -->\n"
		body := existing
		if !strings.HasSuffix(body, "\n\n") {
			if strings.HasSuffix(body, "\n") {
				body += "\n"
			} else {
				body += "\n\n"
			}
		}
		body += stamp + entry + "\n"
		return body, false
	}
	// Existing term — replace through next `### ` or EOF.
	tail := existing[idx+len(header):]
	endRel := strings.Index(tail, "\n### ")
	end := idx + len(header) + len(tail)
	if endRel >= 0 {
		end = idx + len(header) + endRel + 1
	}
	stamp := "<!-- updated " + time.Now().UTC().Format("2006-01-02") + " -->\n"
	return existing[:idx] + stamp + entry + existing[end:], true
}

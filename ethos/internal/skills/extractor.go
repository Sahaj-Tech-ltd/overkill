// Package skills — Voyager-style skill auto-creation (master plan §6.2).
//
// ExtractSkill takes a session transcript and a short title and emits a
// SKILL.md under ~/.overkill/skills/<name>/. The skill captures the procedure
// the user just walked through so the next session can `use the X skill`
// instead of re-deriving it.
//
// We don't run an LLM here — we emit a structured stub that the agent can
// later flesh out via its own provider call. Keeps the extractor cheap and
// deterministic; the agent's existing Run loop handles polishing.
package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// ExtractRequest is the input to a skill-extraction call.
type ExtractRequest struct {
	Name         string   // skill folder name; sanitized
	Description  string   // one-line summary
	Tags         []string // topic tags
	Triggers     []string // phrases that should activate the skill
	Transcript   string   // session text or relevant excerpt
	OutputDir    string   // typically ~/.overkill/skills
	Author       string   // optional
}

// ExtractResult is what was written.
type ExtractResult struct {
	Path    string `json:"path"`
	Name    string `json:"name"`
	Created bool   `json:"created"` // false when the file already existed and was skipped
}

// Extract writes a SKILL.md from the request. Existing skill files are
// preserved (Created=false) so the user never loses hand-edits to a
// previously-extracted skill.
func Extract(req ExtractRequest) (*ExtractResult, error) {
	name := sanitizeName(req.Name)
	if name == "" {
		return nil, fmt.Errorf("skills: invalid name")
	}
	if req.OutputDir == "" {
		return nil, fmt.Errorf("skills: output_dir required")
	}
	dir := filepath.Join(req.OutputDir, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	out := filepath.Join(dir, "SKILL.md")
	if _, err := os.Stat(out); err == nil {
		return &ExtractResult{Path: out, Name: name, Created: false}, nil
	}
	body := renderSkillMarkdown(name, req)
	if err := os.WriteFile(out, []byte(body), 0o644); err != nil {
		return nil, err
	}
	return &ExtractResult{Path: out, Name: name, Created: true}, nil
}

func renderSkillMarkdown(name string, req ExtractRequest) string {
	var b strings.Builder
	fmt.Fprintf(&b, "---\nname: %s\n", name)
	if req.Description != "" {
		fmt.Fprintf(&b, "description: %s\n", req.Description)
	}
	if len(req.Tags) > 0 {
		fmt.Fprintf(&b, "tags: [%s]\n", strings.Join(req.Tags, ", "))
	}
	if len(req.Triggers) > 0 {
		fmt.Fprintf(&b, "triggers: [%s]\n", quoteList(req.Triggers))
	}
	if req.Author != "" {
		fmt.Fprintf(&b, "author: %s\n", req.Author)
	}
	fmt.Fprintf(&b, "version: 0.1.0\n")
	fmt.Fprintf(&b, "extracted_at: %s\n", time.Now().UTC().Format(time.RFC3339))
	b.WriteString("---\n\n")
	fmt.Fprintf(&b, "# %s\n\n", titleize(name))
	if req.Description != "" {
		b.WriteString(req.Description)
		b.WriteString("\n\n")
	}
	b.WriteString("## When to use\n\n")
	if len(req.Triggers) > 0 {
		for _, t := range req.Triggers {
			fmt.Fprintf(&b, "- %s\n", t)
		}
	} else {
		b.WriteString("_(populate manually after extraction.)_\n")
	}
	b.WriteString("\n## Procedure\n\n")
	b.WriteString("_Auto-extracted from session transcript:_\n\n")
	excerpt := condenseTranscript(req.Transcript, 4000)
	if excerpt == "" {
		b.WriteString("_(transcript was empty; flesh this out manually.)_\n")
	} else {
		b.WriteString("```\n")
		b.WriteString(excerpt)
		b.WriteString("\n```\n")
	}
	return b.String()
}

// sanitizeName lowercases, replaces non-alnum with -, trims, and caps length.
var nameInvalid = regexp.MustCompile(`[^a-z0-9-]+`)

func sanitizeName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = nameInvalid.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 60 {
		s = s[:60]
	}
	return s
}

func titleize(s string) string {
	parts := strings.Split(s, "-")
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}

func quoteList(in []string) string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = `"` + strings.ReplaceAll(s, `"`, `'`) + `"`
	}
	return strings.Join(out, ", ")
}

// condenseTranscript trims the transcript head+tail to maxBytes total. The
// goal is to capture the bracketing problem statement and outcome without
// shipping every intermediate tool call.
func condenseTranscript(t string, maxBytes int) string {
	t = strings.TrimSpace(t)
	if len(t) <= maxBytes {
		return t
	}
	half := maxBytes / 2
	return t[:half] + "\n\n... [truncated] ...\n\n" + t[len(t)-half:]
}

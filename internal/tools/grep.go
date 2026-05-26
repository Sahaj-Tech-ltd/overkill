package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type GrepTool struct {
	rootDir string
}

type GrepInput struct {
	Pattern    string `json:"pattern"`
	Include    string `json:"include"`
	MaxResults int    `json:"max_results"`
}

type grepMatch struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Content string `json:"content"`
}

func NewGrepTool(rootDir string) *GrepTool {
	return &GrepTool{rootDir: rootDir}
}

func (g *GrepTool) Name() string {
	return "grep"
}

func (g *GrepTool) Execute(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
	var in GrepInput
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("grep: %w", err)
	}

	if in.Pattern == "" {
		return nil, fmt.Errorf("grep: pattern is required")
	}

	re, err := regexp.Compile(in.Pattern)
	if err != nil {
		return nil, fmt.Errorf("grep: invalid pattern: %w", err)
	}

	maxResults := 50
	if in.MaxResults > 0 {
		maxResults = in.MaxResults
	}

	var includeRe *regexp.Regexp
	if in.Include != "" {
		includeRe, err = regexp.Compile(globToRegex(in.Include))
		if err != nil {
			return nil, fmt.Errorf("grep: invalid include pattern: %w", err)
		}
	}

	var matches []grepMatch
	err = filepath.WalkDir(g.rootDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}

		if ctx.Err() != nil {
			return ctx.Err()
		}

		rel, relErr := filepath.Rel(g.rootDir, path)
		if relErr != nil {
			return nil
		}

		if d.IsDir() {
			if rel == ".git" || strings.HasPrefix(rel, ".git"+string(filepath.Separator)) {
				return filepath.SkipDir
			}
			return nil
		}

		if includeRe != nil && !includeRe.MatchString(filepath.Base(path)) {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return nil
		}
		if info.Size() > 1024*1024 {
			return nil
		}

		bin, err := isBinaryFile(path)
		if err != nil || bin {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		lines := strings.Split(string(data), "\n")
		for i, line := range lines {
			if len(matches) >= maxResults {
				break
			}
			if re.MatchString(line) {
				matches = append(matches, grepMatch{
					File:    rel,
					Line:    i + 1,
					Content: strings.TrimSpace(line),
				})
			}
		}
		return nil
	})
	if err != nil && err != context.Canceled {
		return nil, fmt.Errorf("grep: %w", err)
	}

	out, _ := json.Marshal(matches)
	result := ToolResult{Output: string(out), Success: true}
	raw, _ := json.Marshal(result)
	return raw, nil
}

func globToRegex(pattern string) string {
	p := regexp.QuoteMeta(pattern)
	p = strings.ReplaceAll(p, `\*\*`, ".*")
	p = strings.ReplaceAll(p, `\*`, "[^/]*")
	p = strings.ReplaceAll(p, `\?`, ".")
	return "^" + p + "$"
}

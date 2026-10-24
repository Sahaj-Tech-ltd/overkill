package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
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
		maxResults = min(in.MaxResults, 1000)
	}

	var includeRe *regexp.Regexp
	if in.Include != "" {
		includeRe, err = regexp.Compile(globToRegex(in.Include))
		if err != nil {
			return nil, fmt.Errorf("grep: invalid include pattern: %w", err)
		}
	}

	root, _, err := openWorkspace(g.rootDir, ".")
	if err != nil {
		return nil, fmt.Errorf("grep: %w", err)
	}
	defer root.Close()

	var matches []grepMatch
	retainedBytes := 0
	exceeded := false
	err = filepath.WalkDir(g.rootDir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}

		if ctx.Err() != nil {
			return ctx.Err()
		}

		if exceeded {
			return filepath.SkipAll
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
		// Like rg's default traversal, do not follow file symlinks. Opening via
		// os.Root also prevents a replacement symlink from escaping the workspace.
		if err != nil || !info.Mode().IsRegular() || info.Size() > 1024*1024 {
			return nil
		}
		file, err := root.Open(rel)
		if err != nil {
			return nil
		}
		opened, statErr := file.Stat()
		if statErr != nil || !opened.Mode().IsRegular() || opened.Size() > 1024*1024 {
			_ = file.Close()
			return nil
		}
		data, readErr := io.ReadAll(io.LimitReader(file, 1024*1024+1))
		_ = file.Close()
		if readErr != nil || len(data) > 1024*1024 || bytes.IndexByte(data, 0) >= 0 {
			return nil
		}

		lines := strings.Split(string(data), "\n")
		for i, line := range lines {
			if len(matches) >= maxResults {
				exceeded = true
				break
			}
			if re.MatchString(line) {
				content := boundedGrepLine(line)
				cost := len(content) + len(rel) + 40
				if retainedBytes+cost > 64*1024 {
					exceeded = true
					break
				}
				retainedBytes += cost
				matches = append(matches, grepMatch{
					File:    rel,
					Line:    i + 1,
					Content: content,
				})
			}
		}
		return nil
	})
	if err != nil && err != context.Canceled {
		return nil, fmt.Errorf("grep: %w", err)
	}

	out, _ := json.Marshal(matches)
	result := struct {
		ToolResult
		Truncated bool `json:"truncated"`
	}{ToolResult: ToolResult{Output: string(out), Success: true}, Truncated: exceeded}
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

func boundedGrepLine(line string) string {
	line = strings.TrimSpace(line)
	if len(line) > 4096 {
		return line[:4096] + " … [line truncated]"
	}
	return line
}

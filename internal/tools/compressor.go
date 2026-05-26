package tools

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

type Compressor interface {
	ToolName() string
	Compress(output json.RawMessage) (json.RawMessage, int, error)
}

type CompressorRegistry struct {
	compressors map[string]Compressor
}

func NewCompressorRegistry() *CompressorRegistry {
	cr := &CompressorRegistry{
		compressors: make(map[string]Compressor),
	}
	cr.Register(&ShellCompressor{})
	cr.Register(&GrepCompressor{})
	cr.Register(&GitCompressor{})
	cr.Register(PatchCompressor{})
	// Generic head+tail compressors for tools that can return very large
	// payloads. Conservative thresholds — only fire above 8 KiB.
	for _, name := range []string{
		"fs", "fs_read", "web", "pty_shell",
		"browser_text", "browser_markdown", "browser_eval",
		"lsp_definition", "lsp_references", "lsp_hover", "lsp_symbols",
	} {
		cr.Register(NewHeadTailCompressor(name, 4096, 2048))
	}
	return cr
}

func (cr *CompressorRegistry) Register(c Compressor) error {
	if c == nil {
		return fmt.Errorf("compressor: cannot register nil compressor")
	}
	name := c.ToolName()
	if name == "" {
		return fmt.Errorf("compressor: cannot register compressor with empty name")
	}
	cr.compressors[name] = c
	return nil
}

func (cr *CompressorRegistry) Compress(toolName string, output json.RawMessage) (json.RawMessage, int, error) {
	if output == nil {
		return nil, 0, nil
	}

	c, ok := cr.compressors[toolName]
	if !ok {
		return output, 0, nil
	}

	compressed, saved, err := c.Compress(output)
	if err != nil {
		return output, 0, nil
	}

	return compressed, saved, nil
}

type ShellCompressor struct{}

func (sc *ShellCompressor) ToolName() string {
	return "shell"
}

func (sc *ShellCompressor) Compress(output json.RawMessage) (json.RawMessage, int, error) {
	originalLen := len(output)

	var shellOut ShellOutput
	if err := json.Unmarshal(output, &shellOut); err != nil {
		return output, 0, nil
	}

	stdout := shellOut.Stdout
	if stdout == "" {
		return output, 0, nil
	}

	if strings.Contains(stdout, "diff --git") {
		stdout = extractDiffStats(stdout)
	} else if strings.Contains(stdout, "FAIL") || strings.Contains(stdout, "PASS") || strings.Contains(stdout, "--- FAIL") {
		stdout = extractTestSummary(stdout)
	} else if len(stdout) > 2000 {
		truncated := fmt.Sprintf("... [truncated %d chars]\n%s", len(stdout)-1000, stdout[len(stdout)-1000:])
		stdout = truncated
	}

	shellOut.Stdout = stdout

	compressed, err := json.Marshal(shellOut)
	if err != nil {
		return output, 0, nil
	}

	saved := originalLen - len(compressed)
	if saved < 0 {
		saved = 0
	}

	return json.RawMessage(compressed), saved, nil
}

var diffStatLineRe = regexp.MustCompile(`^\s*[+\-]{3}\s|(?:\|.*\d+\s*[+-]+\s*$)`)

func extractDiffStats(output string) string {
	lines := strings.Split(output, "\n")
	var statLines []string
	added := 0
	removed := 0

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "|") && (strings.Contains(trimmed, "++") || strings.Contains(trimmed, "--")) {
			statLines = append(statLines, line)
		}
		if strings.HasPrefix(trimmed, "+") && !strings.HasPrefix(trimmed, "++") && !strings.HasPrefix(trimmed, "+ ") {
		}
		if strings.HasPrefix(trimmed, "+") && !strings.HasPrefix(trimmed, "++") && !strings.HasPrefix(trimmed, "---") {
			added++
		}
		if strings.HasPrefix(trimmed, "-") && !strings.HasPrefix(trimmed, "--") && !strings.HasPrefix(trimmed, "+++") {
			removed++
		}
	}

	if len(statLines) > 0 {
		return fmt.Sprintf("%s\n+%d/-%d lines changed", strings.Join(statLines, "\n"), added, removed)
	}

	return fmt.Sprintf("+%d/-%d lines changed", added, removed)
}

func extractTestSummary(output string) string {
	lines := strings.Split(output, "\n")
	var kept []string
	var summaryLines []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "FAIL") || strings.HasPrefix(trimmed, "--- FAIL") || strings.Contains(trimmed, "FAILED") {
			kept = append(kept, line)
		}
		if strings.HasPrefix(trimmed, "ok ") || strings.HasPrefix(trimmed, "FAIL") && strings.Contains(trimmed, "test") || strings.Contains(trimmed, "PASS") && !strings.Contains(trimmed, "---") {
			summaryLines = append(summaryLines, line)
		}
	}

	if len(kept) == 0 && len(summaryLines) == 0 {
		return output
	}

	var result []string
	result = append(result, kept...)
	result = append(result, summaryLines...)

	return strings.Join(result, "\n")
}

type GrepCompressor struct{}

func (gc *GrepCompressor) ToolName() string {
	return "grep"
}

func (gc *GrepCompressor) Compress(output json.RawMessage) (json.RawMessage, int, error) {
	originalLen := len(output)

	var toolResult ToolResult
	if err := json.Unmarshal(output, &toolResult); err != nil {
		return output, 0, nil
	}

	content := toolResult.Output
	if content == "" {
		return output, 0, nil
	}

	lines := strings.Split(content, "\n")
	if len(lines) <= 50 {
		return output, 0, nil
	}

	first30 := lines[:30]
	last5 := lines[len(lines)-5:]
	middleCount := len(lines) - 30 - 5

	var truncated strings.Builder
	for _, l := range first30 {
		truncated.WriteString(l)
		truncated.WriteString("\n")
	}
	truncated.WriteString(fmt.Sprintf("... %d more matches\n", middleCount))
	for _, l := range last5 {
		truncated.WriteString(l)
		truncated.WriteString("\n")
	}

	toolResult.Output = strings.TrimRight(truncated.String(), "\n")

	compressed, err := json.Marshal(toolResult)
	if err != nil {
		return output, 0, nil
	}

	saved := originalLen - len(compressed)
	if saved < 0 {
		saved = 0
	}

	return json.RawMessage(compressed), saved, nil
}

type GitCompressor struct{}

func (gc *GitCompressor) ToolName() string {
	return "git"
}

func (gc *GitCompressor) Compress(output json.RawMessage) (json.RawMessage, int, error) {
	originalLen := len(output)

	var toolResult ToolResult
	if err := json.Unmarshal(output, &toolResult); err != nil {
		return output, 0, nil
	}

	content := toolResult.Output
	if content == "" {
		return output, 0, nil
	}

	compressed := compressGitOutput(content)
	if compressed == content {
		return output, 0, nil
	}

	toolResult.Output = compressed

	raw, err := json.Marshal(toolResult)
	if err != nil {
		return output, 0, nil
	}

	saved := originalLen - len(raw)
	if saved < 0 {
		saved = 0
	}

	return json.RawMessage(raw), saved, nil
}

func compressGitOutput(output string) string {
	if strings.Contains(output, "diff --git") {
		return extractGitDiffStats(output)
	}

	if strings.Contains(output, "commit ") {
		return compressGitLog(output)
	}

	return output
}

func extractGitDiffStats(output string) string {
	lines := strings.Split(output, "\n")
	var statLines []string

	for _, line := range lines {
		if strings.Contains(line, " | ") || (strings.HasPrefix(strings.TrimSpace(line), "+") && !strings.HasPrefix(strings.TrimSpace(line), "+++")) || (strings.HasPrefix(strings.TrimSpace(line), "-") && !strings.HasPrefix(strings.TrimSpace(line), "---")) {
		}
		if strings.Contains(line, " | ") {
			statLines = append(statLines, line)
		}
	}

	if len(statLines) > 0 {
		return strings.Join(statLines, "\n")
	}

	return output
}

var commitLineRe = regexp.MustCompile(`^([a-f0-9]{7,40})\s+(.+)$`)

func compressGitLog(output string) string {
	lines := strings.Split(output, "\n")
	var commits []string

	for _, line := range lines {
		if commitLineRe.MatchString(line) {
			commits = append(commits, line)
		}
	}

	if len(commits) <= 5 {
		return output
	}

	last5 := commits[len(commits)-5:]
	moreCount := len(commits) - 5

	var result strings.Builder
	for _, c := range last5 {
		result.WriteString(c)
		result.WriteString("\n")
	}
	result.WriteString(fmt.Sprintf("... %d more", moreCount))

	return strings.TrimRight(result.String(), "\n")
}

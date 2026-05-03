package tools

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Sahaj-Tech-ltd/ethos/internal/security"
)

type PushPreview struct {
	Commits    []CommitInfo
	Files      []FileDiff
	HasSecrets bool
	SecretHits []SecretHit
}

type CommitInfo struct {
	Hash    string
	Message string
	Author  string
	Date    string
}

type FileDiff struct {
	Path      string
	Status    string
	Additions int
	Deletions int
}

type SecretHit struct {
	File    string
	Line    int
	Pattern string
	Preview string
}

func GeneratePushPreview(ctx context.Context, workDir string) (*PushPreview, error) {
	commits, err := getCommits(ctx, workDir)
	if err != nil {
		return nil, fmt.Errorf("push preview: %w", err)
	}

	files, err := getFileDiffs(ctx, workDir)
	if err != nil {
		return nil, fmt.Errorf("push preview: %w", err)
	}

	hits, err := ScanForSecrets(ctx, workDir)
	if err != nil {
		return nil, fmt.Errorf("push preview: %w", err)
	}

	return &PushPreview{
		Commits:    commits,
		Files:      files,
		HasSecrets: len(hits) > 0,
		SecretHits: hits,
	}, nil
}

func ScanForSecrets(ctx context.Context, workDir string) ([]SecretHit, error) {
	diff, err := getDiff(ctx, workDir)
	if err != nil {
		return nil, fmt.Errorf("scan secrets: %w", err)
	}
	if diff == "" {
		return nil, nil
	}

	return parseSecretHits(diff), nil
}

func FormatPreview(preview *PushPreview) string {
	var b strings.Builder

	b.WriteString("╭─────────────────────────────────────────────╮\n")
	b.WriteString("│  PUSH PREVIEW                               │\n")
	b.WriteString("╰─────────────────────────────────────────────╯\n")
	b.WriteString("\n")

	b.WriteString(fmt.Sprintf("Commits (%d):\n", len(preview.Commits)))
	for _, c := range preview.Commits {
		short := c.Hash
		if len(short) > 7 {
			short = short[:7]
		}
		b.WriteString(fmt.Sprintf("  %s %s\n", short, c.Message))
	}

	if len(preview.Files) > 0 {
		b.WriteString("\nFiles changed:\n")
		for _, f := range preview.Files {
			b.WriteString(fmt.Sprintf("  %s  %-40s  +%d  -%d\n", f.Status, f.Path, f.Additions, f.Deletions))
		}
	}

	b.WriteString("\n")
	if preview.HasSecrets {
		b.WriteString(fmt.Sprintf("⚠  %d SECRETS DETECTED:\n", len(preview.SecretHits)))
		for _, h := range preview.SecretHits {
			b.WriteString(fmt.Sprintf("  ✗ %s:%d [%s] %s\n", h.File, h.Line, h.Pattern, h.Preview))
		}
		b.WriteString("\n⚠  BLOCK: Remove secrets before pushing.\n")
	} else {
		b.WriteString("✓  No secrets detected. Safe to push.\n")
	}

	return b.String()
}

const commitFormat = "%H|%s|%an|%ci"

func getCommits(ctx context.Context, workDir string) ([]CommitInfo, error) {
	out, err := runGitCommand(ctx, workDir, "log", "@{u}..HEAD", "--format="+commitFormat)
	if err == nil {
		return parseCommitLog(out), nil
	}

	countStr, err := runGitCommand(ctx, workDir, "rev-list", "--count", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("get commits: %w", err)
	}
	n, _ := strconv.Atoi(strings.TrimSpace(countStr))
	if n <= 0 {
		n = 1
	}

	rangeArg := "-1"
	if n > 1 {
		rangeArg = "HEAD~" + strconv.Itoa(n-1) + "..HEAD"
	}

	out, err = runGitCommand(ctx, workDir, "log", rangeArg, "--format="+commitFormat)
	if err != nil {
		out, err = runGitCommand(ctx, workDir, "log", "-1", "--format="+commitFormat)
		if err != nil {
			return nil, fmt.Errorf("get commits: %w", err)
		}
	}

	return parseCommitLog(out), nil
}

func parseCommitLog(out string) []CommitInfo {
	var commits []CommitInfo
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 4)
		if len(parts) < 4 {
			continue
		}
		commits = append(commits, CommitInfo{
			Hash:    parts[0],
			Message: parts[1],
			Author:  parts[2],
			Date:    parts[3],
		})
	}
	return commits
}

func getDiffRange(ctx context.Context, workDir string) string {
	_, err := runGitCommand(ctx, workDir, "rev-parse", "@{u}")
	if err == nil {
		return "@{u}..HEAD"
	}

	out, err := runGitCommand(ctx, workDir, "rev-list", "--count", "HEAD")
	if err != nil {
		return ""
	}
	count := strings.TrimSpace(out)

	depth := count
	n, parseErr := strconv.Atoi(count)
	if parseErr == nil && n > 1 {
		depth = strconv.Itoa(n - 1)
	}
	return "HEAD~" + depth + "..HEAD"
}

func getFileDiffs(ctx context.Context, workDir string) ([]FileDiff, error) {
	rng := getDiffRange(ctx, workDir)
	if rng == "" {
		return nil, nil
	}

	statOut, err := runGitCommand(ctx, workDir, "diff", "--stat", rng)
	if err != nil {
		return nil, nil
	}

	statusMap := getFileStatuses(ctx, workDir, rng)

	var diffs []FileDiff
	statLineRe := regexp.MustCompile(`^\s*(.+?)\s*\|\s*(\d+)\s*([+\-]+)`)

	for _, line := range strings.Split(strings.TrimSpace(statOut), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, "|") {
			continue
		}

		matches := statLineRe.FindStringSubmatch(line)
		if matches == nil {
			continue
		}

		path := strings.TrimSpace(matches[1])
		additions := strings.Count(matches[3], "+")
		deletions := strings.Count(matches[3], "-")

		status := "M"
		if s, ok := statusMap[path]; ok {
			status = s
		}

		diffs = append(diffs, FileDiff{
			Path:      path,
			Status:    status,
			Additions: additions,
			Deletions: deletions,
		})
	}

	sort.Slice(diffs, func(i, j int) bool {
		return diffs[i].Path < diffs[j].Path
	})

	return diffs, nil
}

func getFileStatuses(ctx context.Context, workDir string, rng string) map[string]string {
	statuses := make(map[string]string)

	out, err := runGitCommand(ctx, workDir, "diff", "--name-status", rng)
	if err != nil {
		return statuses
	}

	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			status := fields[0]
			statuses[fields[1]] = status[:1]
		}
	}

	return statuses
}

func getDiff(ctx context.Context, workDir string) (string, error) {
	rng := getDiffRange(ctx, workDir)
	if rng == "" {
		return "", nil
	}

	out, err := runGitCommand(ctx, workDir, "diff", rng)
	if err != nil {
		return "", fmt.Errorf("get diff: %w", err)
	}
	return out, nil
}

var hunkHeaderRe = regexp.MustCompile(`^@@\s+-\d+(?:,\d+)?\s+\+(\d+)(?:,\d+)?\s+@@`)
var fileHeaderRe = regexp.MustCompile(`^\+\+\+\s+b/(.+)`)

func parseSecretHits(diff string) []SecretHit {
	scanner := security.NewSecretScanner()
	var hits []SecretHit

	var currentFile string
	var currentLine int

	for _, rawLine := range strings.Split(diff, "\n") {
		if fm := fileHeaderRe.FindStringSubmatch(rawLine); fm != nil {
			currentFile = fm[1]
			currentLine = 0
			continue
		}

		if hm := hunkHeaderRe.FindStringSubmatch(rawLine); hm != nil {
			currentLine, _ = strconv.Atoi(hm[1])
			continue
		}

		if !strings.HasPrefix(rawLine, "+") || strings.HasPrefix(rawLine, "++") {
			if strings.HasPrefix(rawLine, "-") {
				continue
			}
			currentLine++
			continue
		}

		currentLine++

		lineContent := rawLine[1:]

		result, err := scanner.Scan(lineContent)
		if err != nil || result == nil || len(result.Findings) == 0 {
			continue
		}

		for _, finding := range result.Findings {
			preview := redactPreview(lineContent, finding.Match)
			if len(preview) > 60 {
				preview = preview[:57] + "..."
			}

			hits = append(hits, SecretHit{
				File:    currentFile,
				Line:    currentLine,
				Pattern: finding.Description,
				Preview: preview,
			})
		}
	}

	return hits
}

func redactPreview(line, match string) string {
	return strings.Replace(line, match, "[REDACTED]", 1)
}

func runGitCommand(ctx context.Context, workDir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = workDir

	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}

	return string(out), nil
}

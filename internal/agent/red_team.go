// Package agent — Red-team test generation (master plan §6.5, Wall 1 extension).
//
// RedTeamTestGen generates adversarial tests for completed code changes.
// Unlike Ouroboros (which reviews code for bugs), RedTeam writes actual
// tests that edge-case the code: error states, race conditions, boundary
// values, security boundaries, and pathological inputs.
//
// Architecture:
//   1. Extract changed files from the phase's tool-call history
//   2. Generate adversarial tests via LLM (separate provider, not the build agent)
//   3. Run the tests (go test / pytest / npm test)
//   4. If any fail → surface failures to self-eval loop for revision
//   5. If all pass → phase clears the red-team gate
//
// This is the "tests that actually work" guarantee from the vision doc.
// Red-team tests MUST pass before a build phase is marked complete.

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"

	"github.com/rs/zerolog/log"
)

// RedTeamConfig wires the red-team test generation engine.
type RedTeamConfig struct {
	// Enabled toggles red-team test generation. When false, phases
	// skip this gate (legacy behavior).
	Enabled bool

	// Provider is the LLM used for test generation. SHOULD be a
	// separate provider from the build agent — the build agent writes
	// code, the red-team agent tries to break it.
	Provider providers.Provider

	// Model for test generation. Use a capable but cost-effective
	// model (e.g., deepseek-v4-flash, claude-haiku).
	Model string

	// MaxTestsPerFile caps the number of adversarial tests generated
	// per changed file to prevent token blowout.
	MaxTestsPerFile int

	// SystemPrompt overrides the default adversarial test generator
	// prompt. When empty, the built-in prompt is used.
	SystemPrompt string

	// RunTests executes the generated tests and returns results.
	// Injectable for testing; default uses the project's test runner.
	RunTests func(ctx context.Context, workdir string, testFiles map[string]string) (*TestRunResult, error)
}

// TestRunResult summarizes a red-team test run.
type TestRunResult struct {
	Passed  bool             `json:"passed"`
	Total   int              `json:"total"`
	Failed  int              `json:"failed"`
	Output  string           `json:"output"`
	Details []TestFileResult `json:"details,omitempty"`
}

// TestFileResult describes one test file's outcome.
type TestFileResult struct {
	File   string `json:"file"`
	Passed bool   `json:"passed"`
	Output string `json:"output"`
}

// RedTeamTestGen generates and runs adversarial tests.
type RedTeamTestGen struct {
	cfg RedTeamConfig
}

// NewRedTeamTestGen creates a red-team test generator.
func NewRedTeamTestGen(cfg RedTeamConfig) *RedTeamTestGen {
	if cfg.MaxTestsPerFile <= 0 {
		cfg.MaxTestsPerFile = 5
	}
	return &RedTeamTestGen{cfg: cfg}
}

// RedTeamResult is the output of one red-team phase gate check.
type RedTeamResult struct {
	Passed      bool              `json:"passed"`
	TestsRun    int               `json:"tests_run"`
	TestsFailed int               `json:"tests_failed"`
	Failures    []string          `json:"failures,omitempty"`
	TestFiles   map[string]string `json:"-"` // filename → content, for revision
	Output      string            `json:"output"`
}

// GenerateAndRun is the main entry point. It extracts changed files from
// the agent's tool-call history, generates adversarial tests, runs them,
// and returns the result.
//
// Returns (nil, nil) when disabled — caller treats this as "gate passed,
// no tests needed." Returns a populated RedTeamResult otherwise.
func (rt *RedTeamTestGen) GenerateAndRun(
	ctx context.Context,
	history []providers.Message,
	workdir string,
	spec string,
) (*RedTeamResult, error) {
	if !rt.cfg.Enabled || rt.cfg.Provider == nil {
		log.Debug().Msg("red-team: disabled or no provider, skipping")
		return nil, nil
	}

	// 1. Extract what files were changed.
	changedFiles := extractChangedFiles(history)
	if len(changedFiles) == 0 {
		log.Debug().Msg("red-team: no changed files detected, skipping")
		return &RedTeamResult{Passed: true}, nil
	}

	log.Info().
		Int("changed_files", len(changedFiles)).
		Msg("red-team: generating adversarial tests")

	// 2. Generate adversarial tests per changed file.
	testFiles, err := rt.generateTests(ctx, changedFiles, spec)
	if err != nil {
		return nil, fmt.Errorf("red-team: test generation failed: %w", err)
	}

	if len(testFiles) == 0 {
		return &RedTeamResult{Passed: true, Output: "no tests generated"}, nil
	}

	// 3. Write test files to disk.
	if err := rt.writeTestFiles(workdir, testFiles); err != nil {
		return nil, fmt.Errorf("red-team: write test files: %w", err)
	}

	// 4. Run the tests.
	runner := rt.cfg.RunTests
	if runner == nil {
		runner = defaultTestRunner
	}

	runResult, err := runner(ctx, workdir, testFiles)
	if err != nil {
		return nil, fmt.Errorf("red-team: test run failed: %w", err)
	}

	log.Info().
		Int("total", runResult.Total).
		Int("failed", runResult.Failed).
		Bool("passed", runResult.Passed).
		Msg("red-team: test run complete")

	// 5. Build result.
	result := &RedTeamResult{
		Passed:      runResult.Passed,
		TestsRun:    runResult.Total,
		TestsFailed: runResult.Failed,
		TestFiles:   testFiles,
		Output:      runResult.Output,
	}

	if !runResult.Passed {
		for _, detail := range runResult.Details {
			if !detail.Passed {
				result.Failures = append(result.Failures,
					fmt.Sprintf("%s: %s", detail.File, detail.Output))
			}
		}
	}

	return result, nil
}

// BuildRevisionContext produces a prompt snippet that feeds red-team
// test failures back into the self-evaluate loop. The build agent sees
// exactly which tests failed and why, so it can fix the issues.
func (rt *RedTeamTestGen) BuildRevisionContext(result *RedTeamResult) string {
	if result == nil || result.Passed {
		return ""
	}

	var b strings.Builder
	b.WriteString("\n## RED-TEAM TEST FAILURES\n")
	b.WriteString(fmt.Sprintf("%d/%d adversarial tests failed. Fix the code to pass ALL tests.\n\n",
		result.TestsFailed, result.TestsRun))

	b.WriteString("### Failing Tests\n")
	for _, failure := range result.Failures {
		b.WriteString(fmt.Sprintf("- %s\n", failure))
	}

	b.WriteString("\n### Generated Test Files (for reference)\n")
	for filename, content := range result.TestFiles {
		b.WriteString(fmt.Sprintf("**%s**:\n```\n%s\n```\n\n", filename, truncate(content, 2000)))
	}

	return b.String()
}

// generateTests calls the red-team LLM to generate adversarial tests.
func (rt *RedTeamTestGen) generateTests(
	ctx context.Context,
	changedFiles map[string]string,
	spec string,
) (map[string]string, error) {
	systemPrompt := rt.cfg.SystemPrompt
	if systemPrompt == "" {
		systemPrompt = `You are an adversarial test engineer. Your job is to BREAK code — find edge cases, error states, race conditions, boundary values, and security vulnerabilities.

For each changed file, generate tests that exercise:
1. Boundary values (empty, nil, zero, max, min)
2. Error paths (invalid inputs, missing dependencies, network failures)
3. Concurrency issues (race conditions, deadlocks, ordering assumptions)
4. Security boundaries (injection, auth bypass, privilege escalation)
5. Edge cases the developer didn't think about

Output format — a JSON object mapping test file paths to test code:
{
  "path/to/file_test.go": "package pkg\n\nfunc TestEdgeCase...",
  "path/to/other_test.py": "def test_boundary..."
}

Test files should follow the project's existing test conventions.
Use the same test framework already in use (go test, pytest, jest, vitest).
Tests MUST compile and run — no syntax errors, no missing imports.`
	}

	userContent := fmt.Sprintf("## Specification\n%s\n\n## Changed Files\n", spec)
	for filename, content := range changedFiles {
		userContent += fmt.Sprintf("### %s\n```\n%s\n```\n\n", filename, truncate(content, 3000))
	}
	userContent += "\nGenerate adversarial tests for these changes. Output valid JSON."

	resp, err := rt.cfg.Provider.Complete(ctx, providers.Request{
		Model:        rt.cfg.Model,
		SystemPrompt: systemPrompt,
		Messages: []providers.Message{
			{Role: "user", Content: userContent},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("red-team: LLM call failed: %w", err)
	}

	// Parse the JSON response into a map of filename → test code.
	var testFiles map[string]string
	content := extractJSON(resp.Content)
	if err := json.Unmarshal([]byte(content), &testFiles); err != nil {
		log.Warn().Err(err).Str("raw", truncate(resp.Content, 500)).Msg("red-team: failed to parse test JSON, retrying with fix prompt")

		// Retry once with a fix prompt.
		fixPrompt := fmt.Sprintf("Your previous response was not valid JSON. The error was: %v. Please output ONLY valid JSON in this format: {\"filename_test.go\": \"test code here\"}", err)
		resp2, err2 := rt.cfg.Provider.Complete(ctx, providers.Request{
			Model:        rt.cfg.Model,
			SystemPrompt: systemPrompt,
			Messages: []providers.Message{
				{Role: "user", Content: userContent},
				{Role: "assistant", Content: resp.Content},
				{Role: "user", Content: fixPrompt},
			},
		})
		if err2 != nil {
			return nil, fmt.Errorf("red-team: retry failed: %w", err2)
		}

		content = extractJSON(resp2.Content)
		if err := json.Unmarshal([]byte(content), &testFiles); err != nil {
			return nil, fmt.Errorf("red-team: still invalid JSON after retry: %w", err)
		}
	}

	// Cap per file.
	if rt.cfg.MaxTestsPerFile > 0 {
		for filename, code := range testFiles {
			if testCount := strings.Count(code, "func Test"); testCount > rt.cfg.MaxTestsPerFile {
				log.Warn().
					Str("file", filename).
					Int("count", testCount).
					Int("max", rt.cfg.MaxTestsPerFile).
					Msg("red-team: test file exceeds cap")
			}
		}
	}

	return testFiles, nil
}

// writeTestFiles writes generated test files to the working directory.
func (rt *RedTeamTestGen) writeTestFiles(workdir string, testFiles map[string]string) error {
	if err := os.MkdirAll(workdir, 0755); err != nil {
		return fmt.Errorf("create workdir: %w", err)
	}
	for filename, code := range testFiles {
		dest := filepath.Join(workdir, filename)
		// Prevent path traversal from LLM-generated filenames.
		if !strings.HasPrefix(filepath.Clean(dest), filepath.Clean(workdir)+string(os.PathSeparator)) {
			continue
		}
		if err := os.WriteFile(dest, []byte(code), 0644); err != nil {
			return fmt.Errorf("write %s: %w", filename, err)
		}
	}
	return nil
}

// extractChangedFiles inspects the agent's tool-call history and returns
// a map of filename → content for files that were created or modified.
func extractChangedFiles(history []providers.Message) map[string]string {
	changed := make(map[string]string)

	for _, msg := range history {
		if msg.Role != "assistant" {
			continue
		}
		for _, tc := range msg.ToolCalls {
			switch tc.Name {
			case "write_file", "patch", "fs_write", "fs_edit":
				var args map[string]interface{}
				if err := json.Unmarshal([]byte(tc.Arguments), &args); err != nil {
					continue
				}
				path, _ := args["path"].(string)
				content, _ := args["content"].(string)

				if path != "" && isTestableFile(path) {
					// For patches, we have old_string + new_string.
					if tc.Name == "patch" && content == "" {
						oldStr, _ := args["old_string"].(string)
						newStr, _ := args["new_string"].(string)
						content = fmt.Sprintf("// PATCH: replaced\n// -%s\n// +%s", oldStr, newStr)
					}
					changed[path] = content
				}
			}
		}
	}
	return changed
}

// isTestableFile returns true for source code files that red-team can test.
func isTestableFile(path string) bool {
	lower := strings.ToLower(path)
	exts := []string{".go", ".py", ".ts", ".tsx", ".js", ".jsx", ".rs", ".java", ".rb"}
	for _, ext := range exts {
		if strings.HasSuffix(lower, ext) && !strings.Contains(lower, "_test.") && !strings.Contains(lower, ".test.") {
			return true
		}
	}
	return false
}

// extractJSON pulls JSON from an LLM response that may have markdown fences.
func extractJSON(raw string) string {
	// Try to extract from ```json fences.
	if idx := strings.Index(raw, "```json"); idx >= 0 {
		start := idx + len("```json")
		if end := strings.Index(raw[start:], "```"); end >= 0 {
			return strings.TrimSpace(raw[start : start+end])
		}
	}
	if idx := strings.Index(raw, "```"); idx >= 0 {
		start := idx + len("```")
		if end := strings.Index(raw[start:], "```"); end >= 0 {
			return strings.TrimSpace(raw[start : start+end])
		}
	}
	// Try bare JSON object.
	raw = strings.TrimSpace(raw)
	if strings.HasPrefix(raw, "{") {
		return raw
	}
	return raw
}

// truncate limits a string to maxLen characters with ellipsis.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "...[truncated]"
}

// --- Default test runner ---

// defaultTestRunner runs tests using the appropriate framework for the project.
func defaultTestRunner(ctx context.Context, workdir string, testFiles map[string]string) (*TestRunResult, error) {
	// Detect framework from file extensions.
	framework := detectTestFramework(testFiles)

	var cmd string
	switch framework {
	case "go":
		cmd = "go test ./... -count=1 -timeout 60s -json"
	case "python":
		cmd = "python -m pytest -x --tb=short -v"
	case "node":
		cmd = "npx vitest run --reporter=verbose"
	case "rust":
		cmd = "cargo test -- --nocapture"
	case "java":
		cmd = "mvn test -B"
	case "ruby":
		cmd = "bundle exec rspec --format documentation"
	default:
		cmd = "go test ./..."
	}

	log.Info().Str("framework", framework).Str("cmd", cmd).Msg("red-team: running tests")

	parts := strings.Fields(cmd)
	c := exec.CommandContext(ctx, parts[0], parts[1:]...)
	c.Dir = workdir
	out, err := c.CombinedOutput()
	output := string(out)

	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			// Test binary ran but some tests failed.
			// Parse output to build per-file failure details.
			details := parseTestOutput(output, testFiles, framework)
			total, failed := countTestResults(details, len(testFiles))
			return &TestRunResult{
				Passed:  false,
				Total:   total,
				Failed:  failed,
				Output:  output,
				Details: details,
			}, nil
		}
		// Infrastructure failure (command not found, workdir missing, etc.).
		return nil, fmt.Errorf("red-team: exec failed: %w", err)
	}

	// All passed — populate details with success entries.
	details := make([]TestFileResult, 0, len(testFiles))
	for filename := range testFiles {
		details = append(details, TestFileResult{
			File:   filename,
			Passed: true,
			Output: "ok",
		})
	}
	return &TestRunResult{
		Passed:  true,
		Total:   len(testFiles),
		Failed:  0,
		Output:  output,
		Details: details,
	}, nil
}

// parseTestOutput extracts per-file test results from framework output.
func parseTestOutput(output string, testFiles map[string]string, framework string) []TestFileResult {
	var details []TestFileResult
	switch framework {
	case "go":
		// Parse go test -json output or plain text for FAIL lines.
		lines := strings.Split(output, "\n")
		for _, line := range lines {
			if strings.Contains(line, "FAIL") && strings.Contains(line, "_test.go") {
				// Extract filename from "FAIL path/to/file_test.go"
				parts := strings.Fields(line)
				for _, p := range parts {
					if strings.Contains(p, "_test.go") || strings.Contains(p, ".go") {
						details = append(details, TestFileResult{
							File:   p,
							Passed: false,
							Output: line,
						})
					}
				}
			}
		}
	case "python":
		lines := strings.Split(output, "\n")
		for _, line := range lines {
			if strings.Contains(line, "FAILED") && strings.Contains(line, ".py") {
				parts := strings.Fields(line)
				if len(parts) >= 2 {
					details = append(details, TestFileResult{
						File:   parts[0],
						Passed: false,
						Output: line,
					})
				}
			}
		}
	case "node":
		lines := strings.Split(output, "\n")
		for _, line := range lines {
			if strings.Contains(line, "FAIL") && (strings.Contains(line, ".test.") || strings.Contains(line, ".spec.")) {
				parts := strings.Fields(line)
				for _, p := range parts {
					if strings.Contains(p, ".test.") || strings.Contains(p, ".spec.") {
						details = append(details, TestFileResult{
							File:   p,
							Passed: false,
							Output: line,
						})
					}
				}
			}
		}
	default:
		// For other frameworks, report all test files as failed with the raw output.
		for filename := range testFiles {
			details = append(details, TestFileResult{
				File:   filename,
				Passed: false,
				Output: output,
			})
		}
	}
	return details
}

// countTestResults tallies total and failed from details, ensuring we report at least len(testFiles).
func countTestResults(details []TestFileResult, fileCount int) (total, failed int) {
	total = fileCount
	failed = 0
	for _, d := range details {
		if !d.Passed {
			failed++
		}
	}
	// If we couldn't parse any details, mark all files as failed.
	if failed == 0 && len(details) == 0 {
		failed = fileCount
	}
	return
}

func detectTestFramework(testFiles map[string]string) string {
	for filename := range testFiles {
		lower := strings.ToLower(filename)
		if strings.HasSuffix(lower, "_test.go") || strings.HasSuffix(lower, ".go") {
			return "go"
		}
		if strings.HasSuffix(lower, "_test.py") || strings.Contains(filename, "test_") {
			return "python"
		}
		if strings.HasSuffix(lower, ".test.ts") || strings.HasSuffix(lower, ".spec.ts") ||
			strings.HasSuffix(lower, ".test.js") || strings.HasSuffix(lower, ".spec.js") ||
			strings.HasSuffix(lower, ".test.jsx") || strings.HasSuffix(lower, ".test.tsx") ||
			strings.HasSuffix(lower, ".spec.jsx") || strings.HasSuffix(lower, ".spec.tsx") {
			return "node"
		}
		if strings.HasSuffix(lower, ".rs") {
			return "rust"
		}
		if strings.HasSuffix(lower, ".java") {
			return "java"
		}
		if strings.HasSuffix(lower, ".rb") {
			return "ruby"
		}
	}
	return "go"
}

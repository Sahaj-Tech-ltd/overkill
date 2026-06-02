package pipeline

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

type Executor struct {
	provider   providers.Provider
	model      string
	maxRetries int
	verify     bool
	renderHTML bool          // auto-generate DeepWiki-style HTML after spec stage
	timeout    time.Duration // M5: top-level pipeline timeout
}

type Config struct {
	Provider   providers.Provider
	Model      string
	MaxRetries int
	Verify     bool
	RenderHTML bool          // auto-generate DeepWiki-style HTML after spec stage
	Timeout    time.Duration // M5: top-level pipeline timeout (0 = no timeout)
}

const defaultPipelineTimeout = 30 * time.Minute

func NewExecutor(cfg Config) *Executor {
	retries := cfg.MaxRetries
	if retries <= 0 {
		retries = 2
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultPipelineTimeout
	}
	return &Executor{
		provider:   cfg.Provider,
		model:      cfg.Model,
		maxRetries: retries,
		verify:     true, // B058: always verify, was cfg.Verify (defaulted to false)
		renderHTML: cfg.RenderHTML,
		timeout:    timeout,
	}
}

func (e *Executor) Run(ctx context.Context, request string) (*PipelineResult, error) {
	if e.provider == nil {
		return nil, fmt.Errorf("pipeline: provider is nil")
	}

	// M5: Apply top-level pipeline timeout.
	if e.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, e.timeout)
		defer cancel()
	}

	start := time.Now()

	stages := []struct {
		stage  Stage
		prompt string
	}{
		{StageSpec, specPrompt()},
		{StageTest, testPrompt()},
		{StageCode, codePrompt()},
		{StageRefactor, refactorPrompt()},
	}

	var results []StageResult
	input := request

	for _, s := range stages {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("pipeline: cancelled before %s stage: %w", s.stage, ctx.Err())
		default:
		}

		var result *StageResult
		var err error

		for attempt := 0; attempt <= e.maxRetries; attempt++ {
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("pipeline: cancelled during %s stage: %w", s.stage, ctx.Err())
			default:
			}

			result, err = e.executeStage(ctx, s.stage, s.prompt, input)
			if err == nil {
				break
			}
			if attempt == e.maxRetries {
				return nil, fmt.Errorf("pipeline: %s stage failed after %d attempts: %w", s.stage, attempt+1, err)
			}
		}

		results = append(results, *result)

		// Auto-render spec HTML for visual plan view.
		if s.stage == StageSpec && e.renderHTML && result.Content != "" {
			e.renderSpecHTML(ctx, result.Content)
		}

		// Verify Test stage: compile-check generated test code
		// against Go syntax so we don't pass syntactically broken tests.
		if s.stage == StageTest && e.verify {
			if errs := e.verifyGeneratedTests(ctx, result.Content); len(errs) > 0 {
				results[len(results)-1].Passed = false
				results[len(results)-1].Errors = errs
			}
		}

		// Verify Code stage: build and test generated implementation.
		// Use named lookups by stage rather than hardcoded indices (H-27 fix).
		if s.stage == StageCode && e.verify {
			testCode := findStageContent(results, StageTest)
			implCode := findStageContent(results, StageCode)
			if testCode != "" && implCode != "" {
				passed, errs, files := e.verifyGoCode(ctx, testCode, implCode)
				results[len(results)-1].Passed = passed
				results[len(results)-1].Errors = errs
				results[len(results)-1].Files = files
			}
		}

		// Verify Refactor stage: re-run tests to confirm refactoring didn't break.
		if s.stage == StageRefactor && e.verify {
			testCode := findStageContent(results, StageTest)
			refCode := findStageContent(results, StageRefactor)
			if testCode != "" && refCode != "" {
				passed, errs, files := e.verifyGoCode(ctx, testCode, refCode)
				results[len(results)-1].Passed = passed
				results[len(results)-1].Errors = errs
				results[len(results)-1].Files = files
			}
		}

		input = result.Content
	}

	totalTime := time.Since(start)
	success := true
	for _, r := range results {
		if len(r.Errors) > 0 {
			success = false
			break
		}
	}

	finalFiles := make(map[string]string)
	lastStage := results[len(results)-1]
	if lastStage.Files != nil {
		finalFiles = lastStage.Files
	}

	return &PipelineResult{
		Stages:     results,
		TotalTime:  totalTime,
		Success:    success,
		FinalFiles: finalFiles,
	}, nil
}

func (e *Executor) RunStage(ctx context.Context, stage Stage, input string) (*StageResult, error) {
	if e.provider == nil {
		return nil, fmt.Errorf("pipeline: provider is nil")
	}
	prompt := stagePrompt(stage)
	if prompt == "" {
		return nil, fmt.Errorf("pipeline: unknown stage %d", stage)
	}

	var result *StageResult
	var err error

	for attempt := 0; attempt <= e.maxRetries; attempt++ {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("pipeline: cancelled during %s stage: %w", stage, ctx.Err())
		default:
		}

		result, err = e.executeStage(ctx, stage, prompt, input)
		if err == nil {
			return result, nil
		}
		if attempt == e.maxRetries {
			return nil, fmt.Errorf("pipeline: %s stage failed after %d attempts: %w", stage, attempt+1, err)
		}
	}

	panic("pipeline: unreachable")
}

func (e *Executor) executeStage(ctx context.Context, stage Stage, systemPrompt, input string) (*StageResult, error) {
	stageStart := time.Now()

	req := providers.Request{
		Model: e.model,
		Messages: []providers.Message{
			{Role: "user", Content: input},
		},
		SystemPrompt: systemPrompt,
	}

	resp, err := e.provider.Complete(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("stage %s: llm call failed: %w", stage, err)
	}

	if resp.Content == "" {
		return nil, fmt.Errorf("stage %s: empty response from model", stage)
	}

	duration := time.Since(stageStart)

	return &StageResult{
		Stage:    stage,
		Content:  resp.Content,
		Passed:   true,
		Duration: duration,
	}, nil
}

func stagePrompt(s Stage) string {
	switch s {
	case StageSpec:
		return specPrompt()
	case StageTest:
		return testPrompt()
	case StageCode:
		return codePrompt()
	case StageRefactor:
		return refactorPrompt()
	default:
		return ""
	}
}

var goBlockRe = regexp.MustCompile("(?s)```go\\s*\\n(.*?)```")

type goBlock struct {
	code     string
	filename string
	isTest   bool
}

func extractGoBlocks(content string) []goBlock {
	var blocks []goBlock

	matches := goBlockRe.FindAllStringSubmatch(content, -1)
	for _, m := range matches {
		code := strings.TrimSpace(m[1])
		if code == "" {
			continue
		}
		isTest := strings.Contains(code, "*testing.T") || strings.Contains(code, "\"_test\"")
		blocks = append(blocks, goBlock{code: code, isTest: isTest})
	}

	if len(blocks) == 0 {
		// Strip fenced code block markers before checking — bare ``` fences
		// (no language tag) would otherwise embed markers in the content,
		// causing looksLikeGo to falsely match the full raw content.
		stripped := stripFences(content)
		if looksLikeGo(stripped) {
			isTest := strings.Contains(stripped, "*testing.T") || strings.Contains(stripped, "\"_test\"")
			blocks = append(blocks, goBlock{code: stripped, isTest: isTest})
		}
	}

	return blocks
}

func looksLikeGo(content string) bool {
	trimmed := strings.TrimSpace(content)
	// Check both raw and trimmed — TrimSpace strips trailing whitespace
	// which can remove the space after "package" in bare "package " input.
	return strings.HasPrefix(content, "package ") ||
		strings.HasPrefix(trimmed, "package ") ||
		strings.Contains(trimmed, "\npackage ")
}

// stripFences removes markdown fenced code block delimiters from content.
// Handles ``` with optional language tag and closing ```.
func stripFences(content string) string {
	// Remove opening fence: ```lang\n or just ```\n
	re := regexp.MustCompile("(?s)^```[a-zA-Z]*\\s*\\n(.*)```\\s*$")
	m := re.FindStringSubmatch(content)
	if m != nil {
		return strings.TrimSpace(m[1])
	}
	return content
}

// findStageContent returns the Content of the first StageResult matching the
// given Stage, or empty string if not found. Used to replace hardcoded stage
// index lookups with named references (H-27 fix).
func findStageContent(results []StageResult, stage Stage) string {
	for _, r := range results {
		if r.Stage == stage {
			return r.Content
		}
	}
	return ""
}

func (e *Executor) verifyGoCode(ctx context.Context, codeContents ...string) (passed bool, errors []string, files map[string]string) {
	var allBlocks []goBlock
	for _, content := range codeContents {
		allBlocks = append(allBlocks, extractGoBlocks(content)...)
	}

	if len(allBlocks) == 0 {
		return true, nil, nil
	}

	// Scan generated code for dangerous directives before writing to disk.
	for _, block := range allBlocks {
		if strings.Contains(block.code, "//go:generate") {
			return false, []string{"verify: rejected go:generate directive in LLM-generated code"}, nil
		}
		if strings.Contains(block.code, "func init()") && (strings.Contains(block.code, "os/exec") || strings.Contains(block.code, "syscall") || strings.Contains(block.code, "\"net\"")) {
			return false, []string{"verify: rejected init() with os/exec, syscall, or net in LLM-generated code"}, nil
		}
	}

	tmpDir, err := os.MkdirTemp("", "overkill-pipeline-")
	if err != nil {
		return false, []string{fmt.Sprintf("verify: create temp dir: %v", err)}, nil
	}
	defer os.RemoveAll(tmpDir)

	files = make(map[string]string)
	for i, block := range allBlocks {
		name := block.filename
		if name == "" {
			if block.isTest {
				name = fmt.Sprintf("file%d_test.go", i)
			} else {
				name = fmt.Sprintf("file%d.go", i)
			}
		}
		path := filepath.Join(tmpDir, name)
		if err := os.WriteFile(path, []byte(block.code), 0600); err != nil {
			return false, []string{fmt.Sprintf("verify: write %s: %v", name, err)}, nil
		}
		files[name] = block.code
	}

	initCmd := exec.CommandContext(ctx, "go", "mod", "init", "temp")
	initCmd.Dir = tmpDir
	if out, err := initCmd.CombinedOutput(); err != nil {
		errMsg := fmt.Sprintf("go mod init: %s", strings.TrimSpace(string(out)))
		return false, []string{errMsg}, files
	}

	buildCmd := exec.CommandContext(ctx, "go", "build", "./...")
	buildCmd.Dir = tmpDir
	if out, err := buildCmd.CombinedOutput(); err != nil {
		errMsg := fmt.Sprintf("go build: %s", strings.TrimSpace(string(out)))
		return false, []string{errMsg}, files
	}

	testCmd := exec.CommandContext(ctx, "go", "test", "./...")
	testCmd.Dir = tmpDir
	if out, err := testCmd.CombinedOutput(); err != nil {
		errMsg := fmt.Sprintf("go test: %s", strings.TrimSpace(string(out)))
		return false, []string{errMsg}, files
	}

	return true, nil, files
}

// renderSpecHTML generates a DeepWiki-style HTML file from the spec content.
func (e *Executor) renderSpecHTML(ctx context.Context, content string) {
	// Generate a short name from the first heading or use default.
	name := "plan"
	title := extractTitle(content)
	if title != "" {
		nameSlug := slugify(title)
		if len(nameSlug) > 50 {
			nameSlug = nameSlug[:50]
		}
		if nameSlug != "" {
			name = nameSlug
		}
	}

	path, err := RenderPlanToFile([]byte(content), RenderConfig{Name: name})
	if err != nil {
		// Non-fatal: don't fail the pipeline because rendering fails.
		fmt.Fprintf(os.Stderr, "pipeline: render spec HTML: %v\n", err)
		return
	}
	fmt.Fprintf(os.Stderr, "pipeline: rendered plan HTML → %s\n", path)
}

// verifyGeneratedTests checks LLM-generated test code for Go syntax
// validity using gofmt (syntax-only, no compilation). Unlike verifyGoCode
// which does full go build+test, this only guards against broken Go that
// won't even parse. At the Test stage the implementation doesn't exist
// yet, so we can't check more than syntax.
func (e *Executor) verifyGeneratedTests(ctx context.Context, content string) []string {
	if content == "" {
		return nil // empty is fine — no Go code to check
	}

	blocks := extractGoBlocks(content)
	if len(blocks) == 0 {
		return nil // no Go code blocks found — not a failure
	}

	// Write test code to a temp directory and run gofmt.
	tmpDir, err := os.MkdirTemp("", "overkill-pipeline-gofmt-*")
	if err != nil {
		return []string{fmt.Sprintf("verify: temp dir: %v", err)}
	}
	defer os.RemoveAll(tmpDir)

	var written []string
	for i, block := range blocks {
		fname := fmt.Sprintf("test_%d.go", i)
		if block.filename != "" {
			fname = block.filename
		}
		target := filepath.Join(tmpDir, fname)
		if err := os.WriteFile(target, []byte(block.code), 0o600); err != nil {
			return []string{fmt.Sprintf("verify: write %s: %v", fname, err)}
		}
		written = append(written, target)
	}

	// gofmt checks syntax. Run on each file individually — gofmt
	// doesn't accept directory arguments the way go vet does.
	for _, fname := range written {
		fmtCmd := exec.CommandContext(ctx, "gofmt", "-e", fname)
		fmtCmd.Dir = tmpDir
		out, err := fmtCmd.CombinedOutput()
		if err != nil {
			return []string{fmt.Sprintf("gofmt (syntax): %s", strings.TrimSpace(string(out)))}
		}
	}

	return nil
}

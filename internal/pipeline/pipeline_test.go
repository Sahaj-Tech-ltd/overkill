package pipeline

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

func newMockProvider(responses []providers.Response, errs []error) *providers.MockProvider {
	callCount := 0
	return providers.NewMockProvider("mock", nil, func(req providers.Request) (providers.Response, error) {
		idx := callCount
		callCount++
		if idx < len(errs) && errs[idx] != nil {
			return providers.Response{}, errs[idx]
		}
		if idx < len(responses) {
			return responses[idx], nil
		}
		return providers.Response{Content: "default response"}, nil
	})
}

func TestExecutor_Run_AllStages(t *testing.T) {
	responses := []providers.Response{
		{Content: "spec output"},
		{Content: "test output"},
		{Content: "code output"},
		{Content: "refactored output"},
	}
	p := newMockProvider(responses, nil)
	exec := NewExecutor(Config{Provider: p, Model: "test-model", MaxRetries: 2})

	result, err := exec.Run(context.Background(), "build a thing")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if len(result.Stages) != 4 {
		t.Fatalf("expected 4 stages, got %d", len(result.Stages))
	}

	expectedStages := []Stage{StageSpec, StageTest, StageCode, StageRefactor}
	for i, sr := range result.Stages {
		if sr.Stage != expectedStages[i] {
			t.Errorf("stage %d: expected %v, got %v", i, expectedStages[i], sr.Stage)
		}
		if !sr.Passed {
			t.Errorf("stage %d: expected passed=true", i)
		}
		if sr.Duration == 0 {
			t.Errorf("stage %d: expected non-zero duration", i)
		}
	}

	if result.TotalTime == 0 {
		t.Error("expected non-zero total time")
	}
}

func TestExecutor_Run_SpecStage(t *testing.T) {
	responses := []providers.Response{
		{Content: "spec output with requirements"},
		{Content: "test output"},
		{Content: "code output"},
		{Content: "refactored output"},
	}
	p := newMockProvider(responses, nil)
	exec := NewExecutor(Config{Provider: p, Model: "test-model"})

	result, err := exec.Run(context.Background(), "build a thing")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if result.Stages[0].Stage != StageSpec {
		t.Errorf("first stage: expected StageSpec, got %v", result.Stages[0].Stage)
	}
	if result.Stages[0].Content != "spec output with requirements" {
		t.Errorf("first stage content: expected 'spec output with requirements', got %q", result.Stages[0].Content)
	}
}

func TestExecutor_Run_TestStage(t *testing.T) {
	responses := []providers.Response{
		{Content: "spec output"},
		{Content: "test output from spec"},
		{Content: "code output"},
		{Content: "refactored output"},
	}
	p := newMockProvider(responses, nil)
	exec := NewExecutor(Config{Provider: p, Model: "test-model"})

	result, err := exec.Run(context.Background(), "build a thing")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if result.Stages[1].Stage != StageTest {
		t.Errorf("second stage: expected StageTest, got %v", result.Stages[1].Stage)
	}
	if result.Stages[1].Content != "test output from spec" {
		t.Errorf("second stage content: expected 'test output from spec', got %q", result.Stages[1].Content)
	}
}

func TestExecutor_Run_CodeStage(t *testing.T) {
	responses := []providers.Response{
		{Content: "spec output"},
		{Content: "test output"},
		{Content: "code output from tests"},
		{Content: "refactored output"},
	}
	p := newMockProvider(responses, nil)
	exec := NewExecutor(Config{Provider: p, Model: "test-model"})

	result, err := exec.Run(context.Background(), "build a thing")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if result.Stages[2].Stage != StageCode {
		t.Errorf("third stage: expected StageCode, got %v", result.Stages[2].Stage)
	}
	if result.Stages[2].Content != "code output from tests" {
		t.Errorf("third stage content: expected 'code output from tests', got %q", result.Stages[2].Content)
	}
}

func TestExecutor_Run_RefactorStage(t *testing.T) {
	responses := []providers.Response{
		{Content: "spec output"},
		{Content: "test output"},
		{Content: "code output"},
		{Content: "refactored output improved"},
	}
	p := newMockProvider(responses, nil)
	exec := NewExecutor(Config{Provider: p, Model: "test-model"})

	result, err := exec.Run(context.Background(), "build a thing")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if result.Stages[3].Stage != StageRefactor {
		t.Errorf("fourth stage: expected StageRefactor, got %v", result.Stages[3].Stage)
	}
	if result.Stages[3].Content != "refactored output improved" {
		t.Errorf("fourth stage content: expected 'refactored output improved', got %q", result.Stages[3].Content)
	}
}

func TestExecutor_Retry(t *testing.T) {
	responses := []providers.Response{
		{Content: "spec output"},
		{Content: "test output"},
		{Content: "code output"},
		{Content: "refactored output"},
	}
	errs := []error{
		nil,
		errors.New("transient failure"),
		nil,
		nil,
		nil,
	}
	p := newMockProvider(responses, errs)
	exec := NewExecutor(Config{Provider: p, Model: "test-model", MaxRetries: 2})

	result, err := exec.Run(context.Background(), "build a thing")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if len(result.Stages) != 4 {
		t.Fatalf("expected 4 stages, got %d", len(result.Stages))
	}
}

func TestExecutor_ContextCancelled(t *testing.T) {
	responses := []providers.Response{
		{Content: "spec output"},
	}
	p := newMockProvider(responses, nil)
	exec := NewExecutor(Config{Provider: p, Model: "test-model"})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := exec.Run(ctx, "build a thing")
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

func TestStage_String(t *testing.T) {
	tests := []struct {
		stage    Stage
		expected string
	}{
		{StageSpec, "spec"},
		{StageTest, "test"},
		{StageCode, "code"},
		{StageRefactor, "refactor"},
		{Stage(99), "unknown"},
	}

	for _, tt := range tests {
		got := tt.stage.String()
		if got != tt.expected {
			t.Errorf("Stage(%d).String() = %q, want %q", tt.stage, got, tt.expected)
		}
	}
}

func TestPipelineResult(t *testing.T) {
	responses := []providers.Response{
		{Content: "spec"},
		{Content: "tests"},
		{Content: "code"},
		{Content: "refactored"},
	}
	p := newMockProvider(responses, nil)
	exec := NewExecutor(Config{Provider: p, Model: "test-model"})

	result, err := exec.Run(context.Background(), "build a thing")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if result.TotalTime <= 0 {
		t.Error("TotalTime should be positive")
	}
	if !result.Success {
		t.Error("expected Success=true when all stages pass")
	}
	if result.Stages[0].Content == "" {
		t.Error("first stage content should not be empty")
	}
}

func TestExecutor_EmptyResponse(t *testing.T) {
	p := providers.NewMockProvider("mock", nil, func(req providers.Request) (providers.Response, error) {
		return providers.Response{Content: ""}, nil
	})
	exec := NewExecutor(Config{Provider: p, Model: "test-model", MaxRetries: 1})

	_, err := exec.Run(context.Background(), "build a thing")
	if err == nil {
		t.Fatal("expected error for empty response")
	}
}

func TestNewExecutor_DefaultRetries(t *testing.T) {
	exec := NewExecutor(Config{
		Provider: providers.NewMockProvider("test", nil, func(req providers.Request) (providers.Response, error) {
			return providers.Response{Content: "ok"}, nil
		}),
		Model: "test",
	})
	if exec.maxRetries != 2 {
		t.Errorf("expected default maxRetries=2, got %d", exec.maxRetries)
	}
}

func TestExecutor_RunStage_SingleStage(t *testing.T) {
	responses := []providers.Response{
		{Content: "spec from single stage"},
	}
	p := newMockProvider(responses, nil)
	exec := NewExecutor(Config{Provider: p, Model: "test-model"})

	result, err := exec.RunStage(context.Background(), StageSpec, "build a thing")
	if err != nil {
		t.Fatalf("RunStage() error = %v", err)
	}

	if result.Stage != StageSpec {
		t.Errorf("expected StageSpec, got %v", result.Stage)
	}
	if result.Content != "spec from single stage" {
		t.Errorf("content = %q, want 'spec from single stage'", result.Content)
	}
}

func TestExecutor_RunStage_UnknownStage(t *testing.T) {
	p := newMockProvider(nil, nil)
	exec := NewExecutor(Config{Provider: p, Model: "test-model"})

	_, err := exec.RunStage(context.Background(), Stage(99), "input")
	if err == nil {
		t.Fatal("expected error for unknown stage")
	}
}

func TestExecutor_RunStage_RetryExhausted(t *testing.T) {
	errs := []error{
		errors.New("fail 1"),
		errors.New("fail 2"),
		errors.New("fail 3"),
	}
	p := newMockProvider(nil, errs)
	exec := NewExecutor(Config{Provider: p, Model: "test-model", MaxRetries: 2})

	_, err := exec.RunStage(context.Background(), StageSpec, "input")
	if err == nil {
		t.Fatal("expected error after retries exhausted")
	}
}

func TestExecutor_Run_AllStagesChaining(t *testing.T) {
	var receivedInputs []string
	p := providers.NewMockProvider("mock", nil, func(req providers.Request) (providers.Response, error) {
		receivedInputs = append(receivedInputs, req.Messages[0].Content)
		return providers.Response{Content: "response for " + req.Messages[0].Content}, nil
	})
	exec := NewExecutor(Config{Provider: p, Model: "test-model"})

	_, err := exec.Run(context.Background(), "original request")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if len(receivedInputs) != 4 {
		t.Fatalf("expected 4 calls, got %d", len(receivedInputs))
	}

	if receivedInputs[0] != "original request" {
		t.Errorf("first input = %q, want 'original request'", receivedInputs[0])
	}
	if receivedInputs[1] != "response for original request" {
		t.Errorf("second input = %q, want 'response for original request'", receivedInputs[1])
	}
	if receivedInputs[2] != "response for response for original request" {
		t.Errorf("third input = %q, want chained response", receivedInputs[2])
	}
}

func TestExecutor_ContextCancelledBetweenStages(t *testing.T) {
	callCount := 0
	p := providers.NewMockProvider("mock", nil, func(req providers.Request) (providers.Response, error) {
		callCount++
		if callCount == 2 {
			return providers.Response{Content: "stop here"}, nil
		}
		return providers.Response{Content: "continue"}, nil
	})
	exec := NewExecutor(Config{Provider: p, Model: "test-model"})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := exec.Run(ctx, "build a thing")
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

func TestExecutor_MaxRetriesZero(t *testing.T) {
	errs := []error{
		errors.New("fail"),
	}
	p := newMockProvider(nil, errs)
	exec := NewExecutor(Config{Provider: p, Model: "test-model", MaxRetries: 0})

	if exec.maxRetries != 2 {
		t.Errorf("expected maxRetries=2 for zero input, got %d", exec.maxRetries)
	}
}

func TestStageResult_Duration(t *testing.T) {
	p := providers.NewMockProvider("mock", nil, func(req providers.Request) (providers.Response, error) {
		time.Sleep(10 * time.Millisecond)
		return providers.Response{Content: "done"}, nil
	})
	exec := NewExecutor(Config{Provider: p, Model: "test-model"})

	result, err := exec.RunStage(context.Background(), StageSpec, "input")
	if err != nil {
		t.Fatalf("RunStage() error = %v", err)
	}

	if result.Duration < 10*time.Millisecond {
		t.Errorf("duration %v should be >= 10ms", result.Duration)
	}
}

func TestExecutor_Run_StageDuration(t *testing.T) {
	responses := []providers.Response{
		{Content: "a"},
		{Content: "b"},
		{Content: "c"},
		{Content: "d"},
	}
	p := newMockProvider(responses, nil)
	exec := NewExecutor(Config{Provider: p, Model: "test-model"})

	result, err := exec.Run(context.Background(), "build")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	for i, sr := range result.Stages {
		if sr.Duration == 0 {
			t.Errorf("stage %d: expected non-zero duration", i)
		}
	}
}

func TestExecutor_Run_FailurePropagation(t *testing.T) {
	errs := []error{
		errors.New("fail"),
		errors.New("fail"),
		errors.New("fail"),
	}
	p := newMockProvider(nil, errs)
	exec := NewExecutor(Config{Provider: p, Model: "test-model", MaxRetries: 2})

	_, err := exec.Run(context.Background(), "build")
	if err == nil {
		t.Fatal("expected error when first stage fails all retries")
	}
}

func TestExecutor_Run_SetsSystemPrompt(t *testing.T) {
	var capturedPrompt string
	p := providers.NewMockProvider("mock", nil, func(req providers.Request) (providers.Response, error) {
		capturedPrompt = req.SystemPrompt
		return providers.Response{Content: "response"}, nil
	})
	exec := NewExecutor(Config{Provider: p, Model: "test-model"})

	_, err := exec.RunStage(context.Background(), StageSpec, "input")
	if err != nil {
		t.Fatalf("RunStage() error = %v", err)
	}

	if capturedPrompt != specPrompt() {
		t.Error("expected spec system prompt to be set")
	}
}

func TestExecutor_Run_SetsCorrectModel(t *testing.T) {
	var capturedModel string
	p := providers.NewMockProvider("mock", nil, func(req providers.Request) (providers.Response, error) {
		capturedModel = req.Model
		return providers.Response{Content: "response"}, nil
	})
	exec := NewExecutor(Config{Provider: p, Model: "gpt-4o"})

	_, err := exec.RunStage(context.Background(), StageSpec, "input")
	if err != nil {
		t.Fatalf("RunStage() error = %v", err)
	}

	if capturedModel != "gpt-4o" {
		t.Errorf("model = %q, want 'gpt-4o'", capturedModel)
	}
}

func TestExecutor_Verify_CodePasses(t *testing.T) {
	responses := []providers.Response{
		{Content: "spec output"},
		{Content: "```go\npackage temp\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(1, 2) != 3 {\n\t\tt.Error(\"1+2 != 3\")\n\t}\n}\n```"},
		{Content: "```go\npackage temp\n\nfunc Add(a, b int) int {\n\treturn a + b\n}\n```"},
		{Content: "```go\npackage temp\n\nfunc Add(a, b int) int {\n\treturn a + b\n}\n```"},
	}
	p := newMockProvider(responses, nil)
	exec := NewExecutor(Config{Provider: p, Model: "test-model", Verify: true})

	result, err := exec.Run(context.Background(), "build an Add function")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	codeStage := result.Stages[2]
	if !codeStage.Passed {
		t.Errorf("Code stage: expected Passed=true, got false. Errors: %v", codeStage.Errors)
	}
	if codeStage.Files == nil || len(codeStage.Files) == 0 {
		t.Error("Code stage: expected Files to be populated")
	}
	if !result.Success {
		t.Errorf("expected PipelineResult.Success=true when verification passes, got Success=%v", result.Success)
		for i, s := range result.Stages {
			t.Logf("  stage %d (%s): Passed=%v Errors=%v", i, s.Stage, s.Passed, s.Errors)
		}
	}
	t.Logf("Code stage files: %v", codeStage.Files)
}

func TestExecutor_Verify_CodeFailsBuild(t *testing.T) {
	responses := []providers.Response{
		{Content: "spec output"},
		{Content: "```go\npackage temp\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(1, 2) != 3 {\n\t\tt.Error(\"1+2 != 3\")\n\t}\n}\n```"},
		{Content: "```go\npackage temp\n\nfunc Add(a, b int) int {\n\treturn a + b  // missing closing brace\n```"},
		{Content: "refactored output"},
	}
	p := newMockProvider(responses, nil)
	exec := NewExecutor(Config{Provider: p, Model: "test-model", Verify: true})

	result, err := exec.Run(context.Background(), "build an Add function")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	codeStage := result.Stages[2]
	if codeStage.Passed {
		t.Error("Code stage: expected Passed=false for broken code, got true")
	}
	if len(codeStage.Errors) == 0 {
		t.Error("Code stage: expected Errors for broken code, got none")
	} else {
		t.Logf("Code stage errors: %v", codeStage.Errors)
	}
	if result.Success {
		t.Error("expected PipelineResult.Success=false when verification fails")
	}
}

func TestExecutor_Verify_CodeFailsTest(t *testing.T) {
	responses := []providers.Response{
		{Content: "spec output"},
		{Content: "```go\npackage temp\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(1, 2) != 4 {\n\t\tt.Error(\"unexpected\")\n\t}\n}\n```"},
		{Content: "```go\npackage temp\n\nfunc Add(a, b int) int {\n\treturn a + b\n}\n```"},
		{Content: "refactored output"},
	}
	p := newMockProvider(responses, nil)
	exec := NewExecutor(Config{Provider: p, Model: "test-model", Verify: true})

	result, err := exec.Run(context.Background(), "build an Add function")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	codeStage := result.Stages[2]
	if codeStage.Passed {
		t.Error("Code stage: expected Passed=false for failing test, got true")
	}
	if len(codeStage.Errors) == 0 {
		t.Error("Code stage: expected Errors for failing test, got none")
	} else {
		t.Logf("Code stage errors: %v", codeStage.Errors)
	}
	if result.Success {
		t.Error("expected PipelineResult.Success=false when verification fails")
	}
}

func TestExecutor_Verify_RefactorReRunsTests(t *testing.T) {
	responses := []providers.Response{
		{Content: "spec output"},
		{Content: "```go\npackage temp\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(1, 2) != 3 {\n\t\tt.Error(\"1+2 != 3\")\n\t}\n}\n```"},
		{Content: "```go\npackage temp\n\nfunc Add(a, b int) int {\n\treturn a + b\n}\n```"},
		{Content: "```go\npackage temp\n\nfunc Add(a, b int) int {\n\treturn a - b\n}\n```"},
	}
	p := newMockProvider(responses, nil)
	exec := NewExecutor(Config{Provider: p, Model: "test-model", Verify: true})

	result, err := exec.Run(context.Background(), "build an Add function")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	codeStage := result.Stages[2]
	if !codeStage.Passed {
		t.Fatalf("Code stage should pass, got errors: %v", codeStage.Errors)
	}

	refactorStage := result.Stages[3]
	if refactorStage.Passed {
		t.Error("Refactor stage: expected Passed=false (refactored Add returns a-b, test expects a+b)")
	}
	if len(refactorStage.Errors) == 0 {
		t.Error("Refactor stage: expected Errors for broken refactor")
	} else {
		t.Logf("Refactor stage errors: %v", refactorStage.Errors)
	}
	if result.Success {
		t.Error("expected PipelineResult.Success=false when refactor verification fails")
	}
}

func TestExecutor_Verify_NoGoCodeDegradesGracefully(t *testing.T) {
	responses := []providers.Response{
		{Content: "spec output"},
		{Content: "test output"},
		{Content: "code output"},
		{Content: "refactored output"},
	}
	p := newMockProvider(responses, nil)
	exec := NewExecutor(Config{Provider: p, Model: "test-model", Verify: true})

	result, err := exec.Run(context.Background(), "build a thing")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	for i, sr := range result.Stages {
		if !sr.Passed {
			t.Errorf("stage %d: expected Passed=true (no Go code to verify), got false. Errors: %v", i, sr.Errors)
		}
	}
	if !result.Success {
		t.Error("expected PipelineResult.Success=true when no Go code to verify")
	}
}

func TestExecutor_Verify_DefaultEnabled(t *testing.T) {
	// B058: verification is always-on by default. This test provides
	// valid Go code that compiles and tests pass — verify should succeed.
	responses := []providers.Response{
		{Content: "spec output"},
		{Content: "```go\npackage temp\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(1, 2) != 3 {\n\t\tt.Error(\"1+2 != 3\")\n\t}\n}\n```"},
		{Content: "```go\npackage temp\n\nfunc Add(a int, b int) int {\n\treturn a + b\n}\n```"},
		{Content: "```go\npackage temp\n\nfunc Add(a int, b int) int {\n\treturn a + b\n}\n```"},
	}
	p := newMockProvider(responses, nil)
	exec := NewExecutor(Config{Provider: p, Model: "test-model", Verify: true})

	result, err := exec.Run(context.Background(), "build an Add function")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	// Code and refactor stages should pass verification with valid code
	for i, sr := range result.Stages {
		if !sr.Passed {
			t.Errorf("stage %d (%s): expected Passed=true with valid code, got false. Errors: %v", i, sr.Stage, sr.Errors)
		}
	}
	if !result.Success {
		t.Errorf("expected PipelineResult.Success=true with valid code, stages=%v", result.Stages)
	}
}

func TestExtractGoBlocks_CodeBlocks(t *testing.T) {
	content := "some text\n```go\npackage main\n\nfunc main() {}\n```\nmore text"
	blocks := extractGoBlocks(content)
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	if !strings.Contains(blocks[0].code, "package main") {
		t.Errorf("expected package main in block, got: %s", blocks[0].code)
	}
}

func TestExtractGoBlocks_NoBlocks(t *testing.T) {
	blocks := extractGoBlocks("just some text, no code here")
	if len(blocks) != 0 {
		t.Errorf("expected 0 blocks, got %d", len(blocks))
	}
}

func TestExtractGoBlocks_BareGo(t *testing.T) {
	blocks := extractGoBlocks("package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"hi\")\n}")
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block from bare Go, got %d", len(blocks))
	}
	if blocks[0].code != "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"hi\")\n}" {
		t.Errorf("unexpected block content: %s", blocks[0].code)
	}
}

func TestExtractGoBlocks_TestFile(t *testing.T) {
	blocks := extractGoBlocks("package temp\n\nimport \"testing\"\n\nfunc TestX(t *testing.T) {}")
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	if !blocks[0].isTest {
		t.Error("expected isTest=true for test code")
	}
}

func TestExtractGoBlocks_MultipleCodeBlocks(t *testing.T) {
	content := "```go\npackage pkg\n\nfunc Foo() int { return 1 }\n```\nand more\n```go\npackage pkg\n\nfunc Bar() int { return 2 }\n```"
	blocks := extractGoBlocks(content)
	if len(blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(blocks))
	}
}

func TestLooksLikeGo(t *testing.T) {
	if !looksLikeGo("package main\nfunc main() {}") {
		t.Error("expected looksLikeGo=true for package main")
	}
	if looksLikeGo("just some text") {
		t.Error("expected looksLikeGo=false for plain text")
	}
	if !looksLikeGo("\n\npackage main\nfunc main() {}") {
		t.Error("expected looksLikeGo=true for indented package")
	}
}

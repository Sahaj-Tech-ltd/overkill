package tools

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestShellCompressor_TruncatesLongOutput(t *testing.T) {
	longOutput := strings.Repeat("a", 3000)
	shellOut := ShellOutput{
		ExitCode:  0,
		Stdout:    longOutput,
		Stderr:    "",
		TimedOut:  false,
		Completed: true,
	}
	raw, _ := json.Marshal(shellOut)

	sc := &ShellCompressor{}
	compressed, saved, err := sc.Compress(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if saved <= 0 {
		t.Error("expected positive savings")
	}

	var result ShellOutput
	if err := json.Unmarshal(compressed, &result); err != nil {
		t.Fatalf("failed to unmarshal compressed: %v", err)
	}
	if !strings.Contains(result.Stdout, "[truncated") {
		t.Error("expected truncation marker in output")
	}
}

func TestShellCompressor_ExtractsDiffStats(t *testing.T) {
	diffOutput := `diff --git a/file.go b/file.go
index abc123..def456 100644
--- a/file.go
+++ b/file.go
@@ -1,5 +1,5 @@
-old line
+new line
 context
-another old
+another new`
	shellOut := ShellOutput{
		ExitCode:  0,
		Stdout:    diffOutput,
		Stderr:    "",
		Completed: true,
	}
	raw, _ := json.Marshal(shellOut)

	sc := &ShellCompressor{}
	compressed, _, err := sc.Compress(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var result ShellOutput
	if err := json.Unmarshal(compressed, &result); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if !strings.Contains(result.Stdout, "lines changed") {
		t.Errorf("expected diff stat summary, got: %s", result.Stdout)
	}
}

func TestShellCompressor_KeepsShortOutputUnchanged(t *testing.T) {
	shortOutput := "hello world"
	shellOut := ShellOutput{
		ExitCode:  0,
		Stdout:    shortOutput,
		Stderr:    "",
		Completed: true,
	}
	raw, _ := json.Marshal(shellOut)

	sc := &ShellCompressor{}
	compressed, saved, err := sc.Compress(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if saved != 0 {
		t.Errorf("expected 0 savings for short output, got %d", saved)
	}

	var result ShellOutput
	if err := json.Unmarshal(compressed, &result); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if result.Stdout != shortOutput {
		t.Errorf("expected output unchanged, got: %s", result.Stdout)
	}
}

func TestShellCompressor_PreservesTestFailures(t *testing.T) {
	testOutput := `=== RUN   TestSomething
--- PASS: TestSomething (0.00s)
=== RUN   TestFailing
    test_test.go:42: expected 5 got 3
--- FAIL: TestFailing (0.00s)
=== RUN   TestOther
--- PASS: TestOther (0.00s)
FAIL
exit status 1
FAIL	github.com/example/pkg	0.123s`
	shellOut := ShellOutput{
		ExitCode:  1,
		Stdout:    testOutput,
		Stderr:    "",
		Completed: true,
	}
	raw, _ := json.Marshal(shellOut)

	sc := &ShellCompressor{}
	compressed, _, err := sc.Compress(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var result ShellOutput
	if err := json.Unmarshal(compressed, &result); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if !strings.Contains(result.Stdout, "FAIL") {
		t.Error("expected FAIL to be preserved in compressed output")
	}
	if !strings.Contains(result.Stdout, "TestFailing") {
		t.Error("expected failing test name to be preserved")
	}
}

func TestGrepCompressor_LimitsLineCount(t *testing.T) {
	var lines []string
	for i := 0; i < 80; i++ {
		lines = append(lines, fmt.Sprintf("file.go:%d:match line %d", i+1, i+1))
	}
	content := strings.Join(lines, "\n")

	toolResult := ToolResult{Output: content, Success: true}
	raw, _ := json.Marshal(toolResult)

	gc := &GrepCompressor{}
	compressed, saved, err := gc.Compress(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if saved <= 0 {
		t.Error("expected positive savings")
	}

	var result ToolResult
	if err := json.Unmarshal(compressed, &result); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if !strings.Contains(result.Output, "45 more matches") {
		t.Errorf("expected '45 more matches' marker, got: %s", result.Output)
	}
}

func TestGitCompressor_ExtractsDiffStatsFromDiffOutput(t *testing.T) {
	diffOutput := `diff --git a/main.go b/main.go
index abc1234..def5678 100644
--- a/main.go
+++ b/main.go
@@ -10,7 +10,7 @@ func main() {
-	fmt.Println("old")
+	fmt.Println("new")
  return
 main.go | 2 +-
 1 file changed, 1 insertion(+), 1 deletion(-)`

	toolResult := ToolResult{Output: diffOutput, Success: true}
	raw, _ := json.Marshal(toolResult)

	gc := &GitCompressor{}
	compressed, _, err := gc.Compress(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var result ToolResult
	if err := json.Unmarshal(compressed, &result); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if !strings.Contains(result.Output, " | ") {
		t.Errorf("expected stat lines with pipe, got: %s", result.Output)
	}
}

func TestPassthroughCompressor_ReturnsRawOutput(t *testing.T) {
	cr := NewCompressorRegistry()
	input := json.RawMessage(`{"output":"raw data","success":true}`)
	compressed, saved, err := cr.Compress("unknown_tool", input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if saved != 0 {
		t.Errorf("expected 0 savings for passthrough, got %d", saved)
	}
	if string(compressed) != string(input) {
		t.Errorf("expected output unchanged for unknown tool")
	}
}

func TestCompressorRegistry_DispatchesToCorrectCompressor(t *testing.T) {
	cr := NewCompressorRegistry()

	longOutput := strings.Repeat("x", 3000)
	shellOut := ShellOutput{ExitCode: 0, Stdout: longOutput, Completed: true}
	raw, _ := json.Marshal(shellOut)

	compressed, saved, err := cr.Compress("shell", raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if saved <= 0 {
		t.Error("expected positive savings from shell compressor dispatch")
	}

	var result ShellOutput
	if err := json.Unmarshal(compressed, &result); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	if !strings.Contains(result.Stdout, "[truncated") {
		t.Error("expected truncation from dispatched shell compressor")
	}
}

func TestCompressorRegistry_FallsBackOnError(t *testing.T) {
	cr := &CompressorRegistry{
		compressors: map[string]Compressor{
			"broken": &brokenCompressor{},
		},
	}

	input := json.RawMessage(`{"some":"data"}`)
	compressed, saved, err := cr.Compress("broken", input)
	if err != nil {
		t.Fatalf("expected no error from fail-open, got: %v", err)
	}
	if saved != 0 {
		t.Errorf("expected 0 savings on error, got %d", saved)
	}
	if string(compressed) != string(input) {
		t.Error("expected raw output on compressor error")
	}
}

func TestCompressorRegistry_ReturnsRawForUnknownTool(t *testing.T) {
	cr := NewCompressorRegistry()
	input := json.RawMessage(`{"output":"something"}`)
	compressed, saved, err := cr.Compress("nonexistent", input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if saved != 0 {
		t.Errorf("expected 0 savings for unknown tool, got %d", saved)
	}
	if string(compressed) != string(input) {
		t.Error("expected raw output for unknown tool")
	}
}

func TestCompressorRegistry_HandlesNilOutput(t *testing.T) {
	cr := NewCompressorRegistry()
	compressed, saved, err := cr.Compress("shell", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if compressed != nil {
		t.Errorf("expected nil output, got %s", string(compressed))
	}
	if saved != 0 {
		t.Errorf("expected 0 savings for nil output, got %d", saved)
	}
}

type brokenCompressor struct{}

func (bc *brokenCompressor) ToolName() string { return "broken" }
func (bc *brokenCompressor) Compress(output json.RawMessage) (json.RawMessage, int, error) {
	return nil, 0, fmt.Errorf("intentional error")
}

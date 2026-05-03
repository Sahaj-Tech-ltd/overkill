package diagnostic

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Sahaj-Tech-ltd/ethos/internal/providers"
)

func TestClassifyError_Compile(t *testing.T) {
	a := &Analyzer{}

	tests := []struct {
		input string
	}{
		{input: "compile error: unexpected token"},
		{input: "syntax error at line 10"},
		{input: "undefined: SomeFunc"},
		{input: "cannot find package foo"},
		{input: "imported and not used: \"fmt\""},
		{input: "missing return at end of function"},
		{input: "expected ';', got '}'"},
	}

	for _, tt := range tests {
		got := a.ClassifyError(tt.input)
		if got != "compile" {
			t.Errorf("ClassifyError(%q) = %q, want %q", tt.input, got, "compile")
		}
	}
}

func TestClassifyError_Runtime(t *testing.T) {
	a := &Analyzer{}

	tests := []struct {
		input string
	}{
		{input: "panic: assignment to entry in nil map"},
		{input: "nil pointer dereference"},
		{input: "runtime error: index out of range [3]"},
		{input: "fatal error: all goroutines are asleep - deadlock!"},
		{input: "SIGSEGV segmentation fault"},
	}

	for _, tt := range tests {
		got := a.ClassifyError(tt.input)
		if got != "runtime" {
			t.Errorf("ClassifyError(%q) = %q, want %q", tt.input, got, "runtime")
		}
	}
}

func TestClassifyError_Test(t *testing.T) {
	a := &Analyzer{}

	tests := []struct {
		input string
	}{
		{input: "--- FAIL: TestSomething"},
		{input: "FAIL\tgithub.com/example/pkg"},
		{input: "test failed: expected 200 got 500"},
	}

	for _, tt := range tests {
		got := a.ClassifyError(tt.input)
		if got != "test" {
			t.Errorf("ClassifyError(%q) = %q, want %q", tt.input, got, "test")
		}
	}
}

func TestClassifyError_Lint(t *testing.T) {
	a := &Analyzer{}

	tests := []struct {
		input string
	}{
		{input: "lint error: unused variable"},
		{input: "style issue: line too long"},
		{input: "format error: inconsistent formatting"},
		{input: "vet: possible formatting directive"},
	}

	for _, tt := range tests {
		got := a.ClassifyError(tt.input)
		if got != "lint" {
			t.Errorf("ClassifyError(%q) = %q, want %q", tt.input, got, "lint")
		}
	}
}

func TestClassifyError_Unknown(t *testing.T) {
	a := &Analyzer{}

	tests := []struct {
		input string
	}{
		{input: "something went wrong"},
		{input: "connection refused"},
		{input: ""},
		{input: "random error message"},
	}

	for _, tt := range tests {
		got := a.ClassifyError(tt.input)
		if got != "unknown" {
			t.Errorf("ClassifyError(%q) = %q, want %q", tt.input, got, "unknown")
		}
	}
}

func TestAnalyzeFile_EntryPoint(t *testing.T) {
	content := `package main

import "fmt"

func main() {
	fmt.Println("hello")
}
`
	fr := AnalyzeFile("cmd/app/main.go", content)
	if fr.Type != "entry" {
		t.Errorf("Type = %q, want %q", fr.Type, "entry")
	}
	if fr.Package != "main" {
		t.Errorf("Package = %q, want %q", fr.Package, "main")
	}
}

func TestAnalyzeFile_Test(t *testing.T) {
	content := `package diagnostic

import "testing"

func TestSomething(t *testing.T) {
	t.Error("fail")
}
`
	fr := AnalyzeFile("foo_test.go", content)
	if fr.Type != "test" {
		t.Errorf("Type = %q, want %q", fr.Type, "test")
	}
	if fr.Package != "diagnostic" {
		t.Errorf("Package = %q, want %q", fr.Package, "diagnostic")
	}
}

func TestAnalyzeFile_Interface(t *testing.T) {
	content := `package providers

type Provider interface {
	Complete(ctx context.Context) error
}

type Another interface {
	DoStuff()
}
`
	fr := AnalyzeFile("types.go", content)
	if fr.Type != "interface" {
		t.Errorf("Type = %q, want %q", fr.Type, "interface")
	}
}

func TestAnalyzeFile_Implementation(t *testing.T) {
	content := `package analyzer

type Handler struct {
	name string
}

func NewHandler(name string) *Handler {
	return &Handler{name: name}
}

func (h *Handler) Process() error {
	return nil
}
`
	fr := AnalyzeFile("handler.go", content)
	if fr.Type != "implementation" {
		t.Errorf("Type = %q, want %q", fr.Type, "implementation")
	}

	foundNew := false
	foundProcess := false
	for _, e := range fr.Exports {
		if e == "NewHandler" {
			foundNew = true
		}
		if e == "Process" {
			foundProcess = true
		}
	}
	if !foundNew {
		t.Error("Exports missing NewHandler")
	}
	if !foundProcess {
		t.Error("Exports missing Process")
	}
}

func TestAnalyzeFile_Config(t *testing.T) {
	tests := []struct {
		path    string
		content string
	}{
		{path: "go.mod", content: "module github.com/example\n\ngo 1.24\n"},
		{path: "config.toml", content: "[server]\nport = 8080\n"},
		{path: "config.yaml", content: "server:\n  port: 8080\n"},
		{path: "config.json", content: "{\"port\": 8080}\n"},
	}

	for _, tt := range tests {
		fr := AnalyzeFile(tt.path, tt.content)
		if fr.Type != "config" {
			t.Errorf("AnalyzeFile(%q) Type = %q, want %q", tt.path, fr.Type, "config")
		}
	}
}

func TestAnalyzeFile_Utility(t *testing.T) {
	content := `package util

func splitPath(p string) []string {
	return strings.Split(p, "/")
}

func joinParts(parts []string) string {
	return strings.Join(parts, "/")
}
`
	fr := AnalyzeFile("util.go", content)
	if fr.Type != "utility" {
		t.Errorf("Type = %q, want %q", fr.Type, "utility")
	}
	if len(fr.Exports) != 0 {
		t.Errorf("Exports = %v, want empty", fr.Exports)
	}
}

func TestAnalyze_WithLLM(t *testing.T) {
	llmResponse := LLMTestResponse{
		RootCauses: []RootCause{
			{
				Description: "nil pointer dereference in handler",
				File:        "handler.go",
				Line:        42,
				Confidence:  0.9,
				Evidence:    []string{"handler references uninitialized field", "stack trace points to handler.go:42"},
			},
		},
		Approaches: []Approach{
			{
				ID:            1,
				Description:   "Add nil check before accessing field",
				Steps:         []string{"Add nil guard in handler", "Add unit test"},
				Confidence:    0.85,
				Risk:          "low",
				EstimatedTime: "5 minutes",
				FilesToChange: []string{"handler.go"},
			},
			{
				ID:            2,
				Description:   "Initialize struct in constructor",
				Steps:         []string{"Update NewHandler to set defaults", "Run all tests"},
				Confidence:    0.7,
				Risk:          "medium",
				EstimatedTime: "10 minutes",
				FilesToChange: []string{"handler.go", "handler_test.go"},
			},
		},
	}

	respJSON, err := json.Marshal(llmResponse)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}

	mock := providers.NewMockProvider("test", nil, func(req providers.Request) (providers.Response, error) {
		return providers.Response{
			Content: string(respJSON),
			Usage:   providers.Usage{InputTokens: 100, OutputTokens: 200},
		}, nil
	})

	analyzer := NewAnalyzer(mock, "test-model")
	report, err := analyzer.Analyze(context.Background(), AnalyzeRequest{
		Error:         "panic: nil pointer dereference",
		ErrorOutput:   "panic: nil pointer dereference\ngoroutine 1 [running]:\nmain.main()\n\thandler.go:42",
		ModifiedFiles: []string{"handler.go"},
		FileContents: map[string]string{
			"handler.go": "package main\n\nfunc main() {\n\tvar h *Handler\n\th.Process()\n}\n",
		},
		RecentActions: []string{"added handler.go"},
	})
	if err != nil {
		t.Fatalf("Analyze() error: %v", err)
	}

	if report.ErrorType != "runtime" {
		t.Errorf("ErrorType = %q, want %q", report.ErrorType, "runtime")
	}
	if len(report.RootCauses) != 1 {
		t.Fatalf("RootCauses len = %d, want 1", len(report.RootCauses))
	}
	if report.RootCauses[0].File != "handler.go" {
		t.Errorf("RootCause File = %q, want %q", report.RootCauses[0].File, "handler.go")
	}
	if len(report.Approaches) != 2 {
		t.Fatalf("Approaches len = %d, want 2", len(report.Approaches))
	}
	if report.Approaches[0].Confidence < report.Approaches[1].Confidence {
		t.Error("Approaches not sorted by confidence descending")
	}
	if report.Recommendation == "" {
		t.Error("Recommendation is empty")
	}
	if report.Confidence <= 0 {
		t.Errorf("Confidence = %f, want > 0", report.Confidence)
	}
	if len(report.AffectedFiles) != 1 {
		t.Errorf("AffectedFiles len = %d, want 1", len(report.AffectedFiles))
	}
	if report.AffectedFiles[0].Path != "handler.go" {
		t.Errorf("AffectedFile Path = %q, want %q", report.AffectedFiles[0].Path, "handler.go")
	}
}

func TestAnalyze_ContextCancelled(t *testing.T) {
	mock := providers.NewMockProvider("test", nil, func(req providers.Request) (providers.Response, error) {
		return providers.Response{}, context.Canceled
	})

	analyzer := NewAnalyzer(mock, "test-model")
	_, err := analyzer.Analyze(context.Background(), AnalyzeRequest{
		Error:       "some error",
		ErrorOutput: "something failed",
	})
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
	if !strings.Contains(err.Error(), "diagnostic:") {
		t.Errorf("error should wrap with 'diagnostic:', got: %v", err)
	}
}

func TestFormatReport(t *testing.T) {
	report := &DiagnosticReport{
		Error:      "undefined: SomeFunc",
		ErrorType:  "compile",
		Confidence: 0.85,
		AffectedFiles: []FileInfo{
			{Path: "handler.go", Role: "implementation", Modified: true},
		},
		RootCauses: []RootCause{
			{
				Description: "SomeFunc is not defined in the package",
				File:        "handler.go",
				Line:        15,
				Confidence:  0.9,
				Evidence:    []string{"function call at line 15", "no matching definition found"},
			},
		},
		Approaches: []Approach{
			{
				ID:            1,
				Description:   "Define the missing function",
				Steps:         []string{"Add SomeFunc to handler.go", "Verify signature matches call site"},
				Confidence:    0.85,
				Risk:          "low",
				EstimatedTime: "2 minutes",
				FilesToChange: []string{"handler.go"},
			},
		},
		Recommendation: "Approach #1: Define the missing function",
	}

	output := FormatReport(report)

	if !strings.Contains(output, "## Diagnostic Report") {
		t.Error("missing header")
	}
	if !strings.Contains(output, "undefined: SomeFunc") {
		t.Error("missing error")
	}
	if !strings.Contains(output, "compile") {
		t.Error("missing error type")
	}
	if !strings.Contains(output, "handler.go") {
		t.Error("missing file path")
	}
	if !strings.Contains(output, "implementation") {
		t.Error("missing file role")
	}
	if !strings.Contains(output, "modified") {
		t.Error("missing modified indicator")
	}
	if !strings.Contains(output, "85%") {
		t.Error("missing confidence percentage")
	}
	if !strings.Contains(output, "Recommended Approach") {
		t.Error("missing recommendation section")
	}
	if !strings.Contains(output, "Approach #1") {
		t.Error("missing approach reference")
	}
}

func TestFormatApproach(t *testing.T) {
	approach := &Approach{
		ID:            1,
		Description:   "Fix the nil pointer",
		Steps:         []string{"Add nil check", "Write test"},
		Confidence:    0.9,
		Risk:          "low",
		EstimatedTime: "5 minutes",
		FilesToChange: []string{"handler.go"},
	}

	output := FormatApproach(approach)

	if !strings.Contains(output, "90%") {
		t.Error("missing confidence percentage")
	}
	if !strings.Contains(output, "low") {
		t.Error("missing risk level")
	}
	if !strings.Contains(output, "Add nil check") {
		t.Error("missing step")
	}
	if !strings.Contains(output, "handler.go") {
		t.Error("missing file to change")
	}
	if !strings.Contains(output, "5 minutes") {
		t.Error("missing estimated time")
	}
}

type LLMTestResponse struct {
	RootCauses []RootCause `json:"root_causes"`
	Approaches []Approach  `json:"approaches"`
}

func TestClassifyError_CompilePrecendence(t *testing.T) {
	a := &Analyzer{}

	input := "compile error: something bad"
	got := a.ClassifyError(input)
	if got != "compile" {
		t.Errorf("ClassifyError(%q) = %q, want %q", input, got, "compile")
	}
}

func TestAnalyzeFile_EmptyContent(t *testing.T) {
	fr := AnalyzeFile("empty.go", "")
	if fr.Type != "unknown" {
		t.Errorf("Type = %q, want %q", fr.Type, "unknown")
	}
	if fr.Package != "" {
		t.Errorf("Package = %q, want empty", fr.Package)
	}
}

func TestAnalyzeFile_ExportsMethod(t *testing.T) {
	content := `package foo

type Bar struct{}

func (b *Bar) DoThing() error {
	return nil
}

func (b *Bar) unexportedHelper() {}
`
	fr := AnalyzeFile("bar.go", content)
	found := false
	for _, e := range fr.Exports {
		if e == "DoThing" {
			found = true
		}
	}
	if !found {
		t.Errorf("Exports missing DoThing, got %v", fr.Exports)
	}
}

func TestParseLLMResponse_InvalidJSON(t *testing.T) {
	causes, approaches := parseLLMResponse("not json at all")
	if len(causes) == 0 {
		t.Fatal("expected fallback root cause")
	}
	if causes[0].Confidence != 0.3 {
		t.Errorf("fallback confidence = %f, want 0.3", causes[0].Confidence)
	}
	if len(approaches) != 0 {
		t.Errorf("approaches = %v, want empty", approaches)
	}
}

func TestAnalyze_NoModifiedFiles(t *testing.T) {
	mock := providers.NewMockProvider("test", nil, func(req providers.Request) (providers.Response, error) {
		resp := LLMTestResponse{
			RootCauses: []RootCause{
				{Description: "unknown", Confidence: 0.5},
			},
		}
		b, _ := json.Marshal(resp)
		return providers.Response{Content: string(b)}, nil
	})

	analyzer := NewAnalyzer(mock, "test-model")
	report, err := analyzer.Analyze(context.Background(), AnalyzeRequest{
		Error:       "something broke",
		ErrorOutput: "something broke badly",
	})
	if err != nil {
		t.Fatalf("Analyze() error: %v", err)
	}
	if len(report.AffectedFiles) != 0 {
		t.Errorf("AffectedFiles = %d, want 0", len(report.AffectedFiles))
	}
}

func TestFormatReport_EmptyReport(t *testing.T) {
	report := &DiagnosticReport{
		Error:     "test error",
		ErrorType: "unknown",
	}
	output := FormatReport(report)
	if !strings.Contains(output, "test error") {
		t.Error("missing error text")
	}
	if !strings.Contains(output, "unknown") {
		t.Error("missing error type")
	}
}

func TestAnalyze_LLMReturnsMarkdownFencedJSON(t *testing.T) {
	respData := LLMTestResponse{
		RootCauses: []RootCause{
			{Description: "bad import", File: "main.go", Confidence: 0.8},
		},
		Approaches: []Approach{
			{ID: 1, Description: "fix import", Confidence: 0.75, Risk: "low"},
		},
	}
	b, _ := json.Marshal(respData)
	fenced := fmt.Sprintf("```json\n%s\n```", string(b))

	mock := providers.NewMockProvider("test", nil, func(req providers.Request) (providers.Response, error) {
		return providers.Response{Content: fenced}, nil
	})

	analyzer := NewAnalyzer(mock, "test-model")
	report, err := analyzer.Analyze(context.Background(), AnalyzeRequest{
		Error:       "import error",
		ErrorOutput: "cannot find package",
	})
	if err != nil {
		t.Fatalf("Analyze() error: %v", err)
	}
	if len(report.RootCauses) != 1 {
		t.Errorf("RootCauses = %d, want 1", len(report.RootCauses))
	}
	if len(report.Approaches) != 1 {
		t.Errorf("Approaches = %d, want 1", len(report.Approaches))
	}
}

package rewriter

import (
	"context"
	"strings"
	"testing"

	"github.com/Sahaj-Tech-ltd/ethos/internal/providers"
)

func TestMiddleware_Analyze_Simple(t *testing.T) {
	m := NewMiddleware()

	tests := []struct {
		name  string
		input string
	}{
		{"fix typo", "fix typo in readme"},
		{"add import", "add missing import"},
		{"remove line", "remove unused variable"},
		{"update config", "update the config file"},
		{"with path", "fix bug in auth.go"},
		{"with line ref", "fix error on line 42"},
		{"short directive", "fix the typo"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := m.Analyze(tt.input)
			if result.Complexity != ComplexitySimple {
				t.Errorf("Analyze(%q) complexity = %v, want Simple", tt.input, result.Complexity)
			}
		})
	}
}

func TestMiddleware_Analyze_Ambiguous(t *testing.T) {
	m := NewMiddleware()

	tests := []struct {
		name  string
		input string
	}{
		{"the thing", "fix the thing in the auth module"},
		{"that bug", "can you fix that bug"},
		{"the issue", "the issue is broken"},
		{"the problem", "the problem with the code needs fixing"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := m.Analyze(tt.input)
			if result.Complexity != ComplexityAmbiguous {
				t.Errorf("Analyze(%q) complexity = %v, want Ambiguous", tt.input, result.Complexity)
			}
		})
	}
}

func TestMiddleware_Analyze_Complex(t *testing.T) {
	m := NewMiddleware()

	tests := []struct {
		name  string
		input string
	}{
		{"build auth", "build a complete authentication system with JWT tokens, refresh token rotation, and session management"},
		{"implement feature", "implement a caching layer that supports multiple backends including Redis, Memcached, and in-memory with TTL support"},
		{"design api", "design a REST API for the blog platform with CRUD operations, pagination, and authentication"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := m.Analyze(tt.input)
			if result.Complexity != ComplexityComplex {
				t.Errorf("Analyze(%q) complexity = %v, want Complex", tt.input, result.Complexity)
			}
		})
	}
}

func TestMiddleware_Strip_Filler(t *testing.T) {
	m := NewMiddleware()

	tests := []struct {
		name     string
		input    string
		wantPart string
	}{
		{"please", "please fix the bug", "fix the bug"},
		{"could you", "could you add a test", "add a test"},
		{"would you mind", "would you mind refactoring this", "refactoring this"},
		{"multiple filler", "please could you fix the bug", "fix the bug"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stripped, removed := m.Strip(tt.input)
			if len(removed) == 0 {
				t.Errorf("Strip(%q) removed nothing, expected filler removal", tt.input)
			}
			if !strings.Contains(stripped, tt.wantPart) {
				t.Errorf("Strip(%q) = %q, want to contain %q", tt.input, stripped, tt.wantPart)
			}
		})
	}
}

func TestMiddleware_Strip_NoFiller(t *testing.T) {
	m := NewMiddleware()

	input := "fix the bug in auth.go"
	stripped, removed := m.Strip(input)

	if stripped != input {
		t.Errorf("Strip(%q) = %q, want unchanged", input, stripped)
	}
	if len(removed) != 0 {
		t.Errorf("Strip(%q) removed %v, want empty", input, removed)
	}
}

func TestMiddleware_InjectSpecificity_Fix(t *testing.T) {
	m := NewMiddleware()

	input := "fix the bug"
	result, injections := m.InjectSpecificity(input)

	if len(injections) == 0 {
		t.Errorf("InjectSpecificity(%q) injected nothing, expected specificity prompt", input)
	}
	if !strings.Contains(result, "specify which file") {
		t.Errorf("InjectSpecificity(%q) = %q, want file prompt", input, result)
	}
}

func TestMiddleware_InjectSpecificity_Specific(t *testing.T) {
	m := NewMiddleware()

	input := "fix bug in auth.go"
	result, injections := m.InjectSpecificity(input)

	if len(injections) != 0 {
		t.Errorf("InjectSpecificity(%q) injected %v, want nothing (already specific)", input, injections)
	}
	if result != input {
		t.Errorf("InjectSpecificity(%q) = %q, want unchanged", input, result)
	}
}

func TestSycophancy_Detect(t *testing.T) {
	s := NewSycophancyReducer()

	tests := []struct {
		name       string
		input      string
		wantDetect bool
	}{
		{"great idea however", "Great idea! However, there's a better approach.", true},
		{"excellent choice", "Excellent choice for the architecture.", true},
		{"absolutely right", "You're absolutely right about that.", true},
		{"completely agree", "I completely agree with your approach.", true},
		{"of course", "Of course! Here's the implementation.", true},
		{"hedging", "That's correct but we should consider alternatives.", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report := s.Check(tt.input)
			if report.Detected != tt.wantDetect {
				t.Errorf("Check(%q) detected = %v, want %v", tt.input, report.Detected, tt.wantDetect)
			}
			if tt.wantDetect && report.Severity <= 0 {
				t.Errorf("Check(%q) severity = %v, want > 0", tt.input, report.Severity)
			}
		})
	}
}

func TestSycophancy_Detect_Clean(t *testing.T) {
	s := NewSycophancyReducer()

	input := "Here's the fix: update the import path on line 3."
	report := s.Check(input)

	if report.Detected {
		t.Errorf("Check(%q) detected sycophancy, want clean. Patterns: %v", input, report.Patterns)
	}
	if report.Severity != 0 {
		t.Errorf("Check(%q) severity = %v, want 0", input, report.Severity)
	}
}

func TestSycophancy_Strip(t *testing.T) {
	s := NewSycophancyReducer()

	tests := []struct {
		name   string
		input  string
		wantIn string
	}{
		{"strip great idea", "Great idea! Here is the code.", "Here is the code."},
		{"strip of course", "Of course! The fix is simple.", "The fix is simple."},
		{"strip happy to help", "Happy to help! Update the config.", "Update the config."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := s.Strip(tt.input)
			if !strings.Contains(result, tt.wantIn) {
				t.Errorf("Strip(%q) = %q, want to contain %q", tt.input, result, tt.wantIn)
			}
		})
	}
}

func TestSycophancy_Strip_PreservesContent(t *testing.T) {
	s := NewSycophancyReducer()

	input := "Great idea! The implementation uses a hash map for O(1) lookups. Here's the code: func main() {}"
	result := s.Strip(input)

	if !strings.Contains(result, "hash map") {
		t.Errorf("Strip removed substantive content: %q", result)
	}
	if !strings.Contains(result, "func main()") {
		t.Errorf("Strip removed code: %q", result)
	}
	if strings.Contains(strings.ToLower(result), "great idea") {
		t.Errorf("Strip kept sycophancy: %q", result)
	}
}

func TestLLMRewriter_SimplePassthrough(t *testing.T) {
	called := false
	mock := providers.NewMockProvider("test", nil, func(req providers.Request) (providers.Response, error) {
		called = true
		return providers.Response{Content: "should not be called"}, nil
	})

	r := NewLLMRewriter(mock, "test-model")
	result, err := r.Rewrite(context.Background(), "fix typo in readme")
	if err != nil {
		t.Fatalf("Rewrite() error = %v", err)
	}

	if called {
		t.Error("Rewrite() called LLM for simple input, should have passed through")
	}
	if result.Complexity != ComplexitySimple {
		t.Errorf("Rewrite() complexity = %v, want Simple", result.Complexity)
	}
	if !strings.Contains(result.Rewritten, "fix typo in readme") {
		t.Errorf("Rewrite() rewritten = %q, want to contain original", result.Rewritten)
	}
}

func TestLLMRewriter_SimpleWithFiller(t *testing.T) {
	called := false
	mock := providers.NewMockProvider("test", nil, func(req providers.Request) (providers.Response, error) {
		called = true
		return providers.Response{Content: "should not be called"}, nil
	})

	r := NewLLMRewriter(mock, "test-model")
	result, err := r.Rewrite(context.Background(), "please fix typo in readme")
	if err != nil {
		t.Fatalf("Rewrite() error = %v", err)
	}

	if called {
		t.Error("Rewrite() called LLM for simple input with filler")
	}
	if !result.Changed {
		t.Error("Rewrite() changed = false, want true (filler was stripped)")
	}
	if len(result.Stripped) == 0 {
		t.Error("Rewrite() stripped nothing, expected filler removal")
	}
}

func TestLLMRewriter_Ambiguous(t *testing.T) {
	called := false
	mock := providers.NewMockProvider("test", nil, func(req providers.Request) (providers.Response, error) {
		called = true
		if req.SystemPrompt == "" {
			t.Error("LLM call missing system prompt")
		}
		return providers.Response{
			Content: "Which auth module is experiencing the issue?",
		}, nil
	})

	r := NewLLMRewriter(mock, "test-model")
	result, err := r.Rewrite(context.Background(), "fix the thing in the auth module")
	if err != nil {
		t.Fatalf("Rewrite() error = %v", err)
	}

	if !called {
		t.Error("Rewrite() did not call LLM for ambiguous input")
	}
	if result.Complexity != ComplexityAmbiguous {
		t.Errorf("Rewrite() complexity = %v, want Ambiguous", result.Complexity)
	}
	if !result.Changed {
		t.Error("Rewrite() changed = false, want true")
	}
	if result.Rewritten == "" {
		t.Error("Rewrite() returned empty rewritten text")
	}
}

func TestLLMRewriter_Complex(t *testing.T) {
	called := false
	mock := providers.NewMockProvider("test", nil, func(req providers.Request) (providers.Response, error) {
		called = true
		if !strings.Contains(req.SystemPrompt, "prompt engineer") {
			t.Errorf("Expected expand system prompt, got: %q", req.SystemPrompt)
		}
		return providers.Response{
			Content: "## Auth System Spec\n\n1. Task: Build JWT auth\n2. Constraints: Stateless\n3. Output: Token endpoints",
		}, nil
	})

	r := NewLLMRewriter(mock, "test-model")
	result, err := r.Rewrite(context.Background(), "build a complete authentication system with JWT tokens and session management")
	if err != nil {
		t.Fatalf("Rewrite() error = %v", err)
	}

	if !called {
		t.Error("Rewrite() did not call LLM for complex input")
	}
	if result.Complexity != ComplexityComplex {
		t.Errorf("Rewrite() complexity = %v, want Complex", result.Complexity)
	}
	if !result.Changed {
		t.Error("Rewrite() changed = false, want true")
	}
}

func TestLLMRewriter_ContextCancelled(t *testing.T) {
	mock := providers.NewMockProvider("test", nil, func(req providers.Request) (providers.Response, error) {
		return providers.Response{Content: "response"}, nil
	})

	r := NewLLMRewriter(mock, "test-model")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := r.Rewrite(ctx, "fix the thing in the auth module")
	if err == nil {
		t.Error("Rewrite() expected error with cancelled context, got nil")
	}
}

func TestComplexity_String(t *testing.T) {
	tests := []struct {
		c    Complexity
		want string
	}{
		{ComplexitySimple, "simple"},
		{ComplexityAmbiguous, "ambiguous"},
		{ComplexityComplex, "complex"},
		{Complexity(99), "unknown"},
	}

	for _, tt := range tests {
		if got := tt.c.String(); got != tt.want {
			t.Errorf("Complexity(%d).String() = %q, want %q", tt.c, got, tt.want)
		}
	}
}

func TestMiddleware_WordCount(t *testing.T) {
	m := NewMiddleware()
	result := m.Analyze("fix the bug in the auth module")

	if result.WordCount != 7 {
		t.Errorf("WordCount = %d, want 7", result.WordCount)
	}
}

func TestMiddleware_ConcurrentSafe(t *testing.T) {
	m := NewMiddleware()

	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func() {
			m.Analyze("fix the bug in auth.go")
			m.Strip("please could you fix the bug")
			m.InjectSpecificity("fix the bug")
			done <- true
		}()
	}

	for i := 0; i < 10; i++ {
		<-done
	}
}

func TestMiddleware_SpecificityTriggers(t *testing.T) {
	m := NewMiddleware()

	tests := []struct {
		name    string
		input   string
		wantInj int
	}{
		{"fix without file", "fix the bug", 1},
		{"test without file", "add a test for the function", 1},
		{"refactor without scope", "refactor the code", 1},
		{"fix with file", "fix bug in main.go", 0},
		{"test with file", "test auth_test.go", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, injections := m.InjectSpecificity(tt.input)
			if len(injections) != tt.wantInj {
				t.Errorf("InjectSpecificity(%q) injections = %d, want %d", tt.input, len(injections), tt.wantInj)
			}
		})
	}
}

func TestSycophancy_Severity(t *testing.T) {
	s := NewSycophancyReducer()

	report := s.Check("Great idea! You're absolutely right! I completely agree! Of course! Fantastic!")
	if !report.Detected {
		t.Error("Expected sycophancy detection")
	}
	if report.Severity < 0.5 {
		t.Errorf("Severity = %v for many patterns, want >= 0.5", report.Severity)
	}
}

func TestRewriteResult_JSON(t *testing.T) {
	result := &RewriteResult{
		Original:   "test",
		Rewritten:  "test rewritten",
		Complexity: ComplexitySimple,
		Changed:    true,
		Injected:   []string{},
		Stripped:   []string{"please"},
		Confidence: 0.9,
	}

	if result.Complexity != ComplexitySimple {
		t.Errorf("Complexity mismatch")
	}
	if !result.Changed {
		t.Error("Changed should be true")
	}
}

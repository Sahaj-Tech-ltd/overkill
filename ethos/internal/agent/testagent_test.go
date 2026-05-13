package agent

import (
	"errors"
	"strings"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

func TestNewTestAgent_SetsFields(t *testing.T) {
	mock := providers.NewMockProvider("test", nil, func(req providers.Request) (providers.Response, error) {
		return providers.Response{Content: ""}, nil
	})
	ta := NewTestAgent(mock, "gpt-4")
	if ta.provider == nil {
		t.Error("NewTestAgent(): provider is nil")
	}
	if ta.model != "gpt-4" {
		t.Errorf("NewTestAgent(): model = %q, want %q", ta.model, "gpt-4")
	}
}

func TestGenerateTests_CallsProvider(t *testing.T) {
	var capturedReq providers.Request
	mock := providers.NewMockProvider("test", nil, func(req providers.Request) (providers.Response, error) {
		capturedReq = req
		return providers.Response{Content: "func TestX(t *testing.T) {}"}, nil
	})
	ta := NewTestAgent(mock, "gpt-4")

	spec := TestSpec{
		Description: "unit test a feature",
		FilesToTest: []string{"foo.go", "bar.go"},
		SpecContent: "should return error on nil input",
		Language:    "go",
	}

	_, err := ta.GenerateTests(t.Context(), spec)
	if err != nil {
		t.Fatalf("GenerateTests(): %v", err)
	}

	if capturedReq.Model != "gpt-4" {
		t.Errorf("request model = %q, want %q", capturedReq.Model, "gpt-4")
	}
	if len(capturedReq.Messages) != 1 {
		t.Fatalf("request messages len = %d, want 1", len(capturedReq.Messages))
	}
}

func TestGenerateTests_ReturnsTestCode(t *testing.T) {
	expectedCode := "func TestX(t *testing.T) { t.Log(\"pass\") }"
	mock := providers.NewMockProvider("test", nil, func(req providers.Request) (providers.Response, error) {
		return providers.Response{Content: expectedCode}, nil
	})
	ta := NewTestAgent(mock, "gpt-4")

	spec := TestSpec{
		Description: "basic test",
		FilesToTest: []string{"main.go"},
		SpecContent: "should handle input",
		Language:    "go",
	}

	result, err := ta.GenerateTests(t.Context(), spec)
	if err != nil {
		t.Fatalf("GenerateTests(): %v", err)
	}
	if result != expectedCode {
		t.Errorf("GenerateTests() = %q, want %q", result, expectedCode)
	}
}

func TestGenerateTests_SpecNotConversation(t *testing.T) {
	var capturedReq providers.Request
	mock := providers.NewMockProvider("test", nil, func(req providers.Request) (providers.Response, error) {
		capturedReq = req
		return providers.Response{Content: "tests"}, nil
	})
	ta := NewTestAgent(mock, "gpt-4")

	spec := TestSpec{
		Description: "verify auth flow",
		FilesToTest: []string{"auth.go"},
		SpecContent: "should reject expired tokens",
		Language:    "go",
	}

	_, err := ta.GenerateTests(t.Context(), spec)
	if err != nil {
		t.Fatalf("GenerateTests(): %v", err)
	}

	prompt := capturedReq.Messages[0].Content

	if !strings.Contains(prompt, spec.SpecContent) {
		t.Error("prompt does not contain spec content")
	}
	if !strings.Contains(prompt, "auth.go") {
		t.Error("prompt does not contain file paths")
	}
	if !strings.Contains(prompt, "You do NOT know how the code was implemented") {
		t.Error("prompt missing independence instruction")
	}
	if !strings.Contains(prompt, "Language: go") {
		t.Error("prompt missing language directive")
	}
}

func TestGenerateTests_PropagatesError(t *testing.T) {
	mockErr := errors.New("provider unavailable")
	mock := providers.NewMockProvider("test", nil, func(req providers.Request) (providers.Response, error) {
		return providers.Response{}, mockErr
	})
	ta := NewTestAgent(mock, "gpt-4")

	spec := TestSpec{
		Description: "any",
		FilesToTest: []string{"a.go"},
		SpecContent: "do stuff",
		Language:    "go",
	}

	_, err := ta.GenerateTests(t.Context(), spec)
	if err == nil {
		t.Fatal("GenerateTests() expected error, got nil")
	}
	if !strings.Contains(err.Error(), mockErr.Error()) {
		t.Errorf("GenerateTests() error = %q, want containing %q", err.Error(), mockErr.Error())
	}
}

func TestValidateTests_ReturnsReview(t *testing.T) {
	expectedReview := "Tests look good."
	mock := providers.NewMockProvider("test", nil, func(req providers.Request) (providers.Response, error) {
		return providers.Response{Content: expectedReview}, nil
	})
	ta := NewTestAgent(mock, "gpt-4")

	review, err := ta.ValidateTests(t.Context(), "func TestX(t *testing.T) {}", []string{})
	if err != nil {
		t.Fatalf("ValidateTests(): %v", err)
	}
	if review != expectedReview {
		t.Errorf("ValidateTests() = %q, want %q", review, expectedReview)
	}
}

func TestValidateTests_SendsTestCode(t *testing.T) {
	var capturedReq providers.Request
	mock := providers.NewMockProvider("test", nil, func(req providers.Request) (providers.Response, error) {
		capturedReq = req
		return providers.Response{Content: "review"}, nil
	})
	ta := NewTestAgent(mock, "gpt-4")

	testCode := "func TestX(t *testing.T) { t.Error(\"fail\") }"
	implFiles := []string{"file: main.go\npackage main\nfunc Add(a,b int) int { return a+b }"}

	_, err := ta.ValidateTests(t.Context(), testCode, implFiles)
	if err != nil {
		t.Fatalf("ValidateTests(): %v", err)
	}

	prompt := capturedReq.Messages[0].Content

	if !strings.Contains(prompt, testCode) {
		t.Error("validate prompt does not contain test code")
	}
	if !strings.Contains(prompt, implFiles[0]) {
		t.Error("validate prompt does not contain implementation files")
	}
	if !strings.Contains(prompt, "implementation details instead of behavior") {
		t.Error("validate prompt missing review criteria")
	}
}

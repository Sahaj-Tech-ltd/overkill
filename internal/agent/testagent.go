package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

type TestAgent struct {
	provider providers.Provider
	model    string
}

type TestSpec struct {
	Description string
	FilesToTest []string
	SpecContent string
	Language    string
}

func NewTestAgent(provider providers.Provider, model string) *TestAgent {
	return &TestAgent{
		provider: provider,
		model:    model,
	}
}

func (ta *TestAgent) GenerateTests(ctx context.Context, spec TestSpec) (string, error) {
	prompt := fmt.Sprintf(
		"You are a test engineer. You have a spec and file paths. Write tests that verify the spec. You do NOT know how the code was implemented — you only know what it should do.\n\nLanguage: %s\nFiles to test: %s\nSpec: %s\nDescription: %s\n\nWrite complete, runnable tests. Include edge cases.",
		spec.Language,
		strings.Join(spec.FilesToTest, ", "),
		spec.SpecContent,
		spec.Description,
	)

	resp, err := ta.provider.Complete(ctx, providers.Request{
		Model: ta.model,
		Messages: []providers.Message{
			{Role: "user", Content: prompt},
		},
	})
	if err != nil {
		return "", fmt.Errorf("testagent: generate tests: %w", err)
	}

	return resp.Content, nil
}

func (ta *TestAgent) ValidateTests(ctx context.Context, testCode string, implFiles []string) (string, error) {
	prompt := fmt.Sprintf(
		"You are a test reviewer. Check if these tests correctly verify the spec given the actual implementation files.\n\nTest code:\n%s\n\nImplementation files:\n%s\n\nIdentify any tests that:\n1. Test implementation details instead of behavior\n2. Are overly coupled to specific code patterns\n3. Would break on valid refactors\nReturn a brief review. If tests are good, say \"Tests look good.\"",
		testCode,
		strings.Join(implFiles, "\n"),
	)

	resp, err := ta.provider.Complete(ctx, providers.Request{
		Model: ta.model,
		Messages: []providers.Message{
			{Role: "user", Content: prompt},
		},
	})
	if err != nil {
		return "", fmt.Errorf("testagent: validate tests: %w", err)
	}

	return resp.Content, nil
}

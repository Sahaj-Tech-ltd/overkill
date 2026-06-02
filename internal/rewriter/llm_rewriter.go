package rewriter

import (
	"context"
	"fmt"
	"strings"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

type LLMRewriter struct {
	provider   providers.Provider
	model      string
	middleware *Middleware
	reducer    *SycophancyReducer
}

func NewLLMRewriter(provider providers.Provider, model string) *LLMRewriter {
	return &LLMRewriter{
		provider:   provider,
		model:      model,
		middleware: NewMiddleware(),
		reducer:    NewSycophancyReducer(),
	}
}

// RewritePrompt is a thin convenience wrapper around Rewrite that returns just
// the rewritten string, suitable for middleware-style use. Falls back to the
// original input on error so a misbehaving rewriter never blocks the agent.
func (r *LLMRewriter) RewritePrompt(ctx context.Context, input string) (string, error) {
	if r == nil {
		return input, nil
	}
	res, err := r.Rewrite(ctx, input)
	if err != nil {
		return input, err
	}
	if res == nil || res.Rewritten == "" {
		return input, nil
	}
	return res.Rewritten, nil
}

func (r *LLMRewriter) Rewrite(ctx context.Context, input string) (*RewriteResult, error) {
	analysis := r.middleware.Analyze(input)

	stripped, strippedItems := r.middleware.Strip(input)
	injectedStr, injectedItems := r.middleware.InjectSpecificity(stripped)

	result := &RewriteResult{
		Original:   input,
		Complexity: analysis.Complexity,
		Stripped:   strippedItems,
		Injected:   injectedItems,
		Confidence: analysis.Confidence,
	}

	switch analysis.Complexity {
	case ComplexitySimple:
		result.Rewritten = injectedStr
		result.Changed = stripped != input || len(injectedItems) > 0
		return result, nil

	case ComplexityAmbiguous:
		clarified, err := r.analyzeWithLLM(ctx, stripped, false)
		if err != nil {
			return nil, fmt.Errorf("rewriter: ambiguous analysis: %w", err)
		}
		result.Rewritten = clarified
		result.Changed = true
		return result, nil

	case ComplexityComplex:
		expanded, err := r.analyzeWithLLM(ctx, stripped, true)
		if err != nil {
			return nil, fmt.Errorf("rewriter: complex expansion: %w", err)
		}
		result.Rewritten = expanded
		result.Changed = true
		return result, nil

	default:
		result.Rewritten = stripped
		result.Changed = stripped != input
		return result, nil
	}
}

func (r *LLMRewriter) analyzeWithLLM(ctx context.Context, input string, expand bool) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}

	var systemPrompt string
	if expand {
		systemPrompt = "You are a prompt engineer. Expand this coding request into a structured spec with: 1) Task description 2) Constraints 3) Expected output 4) Edge cases to consider. Keep it concise. Do NOT add filler or praise. Output only the expanded spec."
	} else {
		systemPrompt = "You are a prompt clarifier. Given an ambiguous coding request, produce a clarifying question. Output ONLY the question, nothing else. Max 2 sentences."
	}

	resp, err := r.provider.Complete(ctx, providers.Request{
		Model:        r.model,
		SystemPrompt: systemPrompt,
		Messages: []providers.Message{
			{Role: "user", Content: input},
		},
		MaxTokens:   1024,
		Temperature: 0.3,
	})
	if err != nil {
		return "", fmt.Errorf("llm call: %w", err)
	}

	cleaned := r.reducer.Strip(resp.Content)
	return strings.TrimSpace(cleaned), nil
}

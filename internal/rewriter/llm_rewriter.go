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
	guard      *antiPatternGuard
	options    Options
}

func NewLLMRewriter(provider providers.Provider, model string) *LLMRewriter {
	// Preserve the established constructor's transformation defaults. Runtime
	// config callers use NewConfiguredRewriter for explicit independent flags.
	return NewWithOptions(provider, model, Options{Enabled: true, StripSycophancy: true, AntiPatternGuard: true, LLMRewrite: true})
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
	if !r.options.Enabled {
		return &RewriteResult{Original: input, Rewritten: input, Confidence: 1}, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	local := input
	var strippedItems, injectedItems []string
	if r.options.StripSycophancy {
		local, strippedItems = r.middleware.Strip(local)
		reduced := local
		if r.reducer.Check(local).Detected {
			reduced = r.reducer.Strip(local)
		}
		if reduced != local {
			strippedItems = append(strippedItems, "sycophancy")
		}
		local = reduced
	}
	analysis := r.middleware.Analyze(local)
	var guardNotes []string
	if r.options.AntiPatternGuard {
		local, injectedItems = r.middleware.InjectSpecificity(local)
		guardNotes = r.guard.notes(input)
		local = appendGuardNotes(local, guardNotes)
		injectedItems = append(injectedItems, guardNotes...)
	}
	result := &RewriteResult{Original: input, Rewritten: local, Complexity: analysis.Complexity, Stripped: strippedItems, Injected: injectedItems, Confidence: analysis.Confidence}
	if r.options.LLMRewrite && analysis.Complexity != ComplexitySimple {
		if r.provider == nil {
			return nil, fmt.Errorf("rewriter: LLM rewriting enabled without a provider")
		}
		rewritten, err := r.analyzeWithLLM(ctx, local, analysis.Complexity == ComplexityComplex)
		if err != nil {
			return nil, fmt.Errorf("rewriter: %s analysis: %w", analysis.Complexity, err)
		}
		// An empty remote response is not a meaningful rewrite.
		if rewritten != "" {
			result.Rewritten = appendGuardNotes(rewritten, guardNotes)
		}
	}
	result.Changed = result.Rewritten != input
	return result, nil
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

	cleaned := resp.Content
	if r.options.StripSycophancy {
		cleaned = r.reducer.Strip(cleaned)
	}
	return strings.TrimSpace(cleaned), nil
}

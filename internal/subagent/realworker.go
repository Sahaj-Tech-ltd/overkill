// Package subagent includes a text-only worker using an OpenAI-compatible API.
package subagent

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

// XIAOMI_BASE_URL is the Xiaomi Mimo API endpoint.
const XIAOMI_BASE_URL = "https://token-plan-sgp.xiaomimimo.com/v1"

// RealWorkerConfig configures a real LLM-backed worker.
type RealWorkerConfig struct {
	Tools     []string // explicit tool capabilities requested by this task
	Goal      string
	Context   string
	MaxSteps  int
	Timeout   time.Duration
	Model     string // from SmartRouter.Route().ModelID or fallback "mimo-v2-pro"
	Provider  string // from SmartRouter.Route().Provider or fallback "xiaomi"
	TaskIndex int    // index assigned by the manager for result tracking
	MaxTokens int    // completion limit override; 0 uses model/provider default
}

// RealWorker performs one text-only completion from the supplied context. It
// cannot inspect the workspace or execute tools; those tasks require a contract
// StepDriver configured by the application.
type RealWorker struct {
	cfg      RealWorkerConfig
	provider providers.Provider
}

// NewRealWorker creates a RealWorker using the available API key for the
// provider. Falls back to XIAOMI_API_KEY for "xiaomi"; supports
// DEEPSEEK_API_KEY for "deepseek". Returns an error when no key is
// available for the requested provider.
func NewRealWorker(cfg RealWorkerConfig) (*RealWorker, error) {
	if cfg.Model == "" {
		if m := os.Getenv("OVERKILL_SUBAGENT_MODEL"); m != "" {
			cfg.Model = m
		} else {
			cfg.Model = "mimo-v2-pro"
		}
	}
	if cfg.Provider == "" {
		if p := os.Getenv("OVERKILL_SUBAGENT_PROVIDER"); p != "" {
			cfg.Provider = p
		} else {
			cfg.Provider = "xiaomi"
		}
	}

	apiKey, baseURL, err := resolveProviderCreds(cfg.Provider)
	if err != nil {
		return nil, err
	}

	p, err := providers.NewProvider(providers.FactoryConfig{
		Name:    cfg.Provider,
		Type:    "custom",
		APIKey:  apiKey,
		BaseURL: baseURL,
		Models: []providers.Model{
			{
				ID:             cfg.Model,
				Name:           cfg.Model,
				SupportsVision: false,
				SupportsTools:  true,
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("subagent: create provider: %w", err)
	}

	return &RealWorker{
		cfg:      cfg,
		provider: p,
	}, nil
}

// resolveProviderCreds returns (apiKey, baseURL, error) for a known provider.
// Extend this map as more providers get API keys.
func resolveProviderCreds(provider string) (string, string, error) {
	switch provider {
	case "xiaomi", "custom:xiaomi":
		key := os.Getenv("XIAOMI_API_KEY")
		if key == "" {
			return "", "", fmt.Errorf("subagent: XIAOMI_API_KEY not set")
		}
		if u := os.Getenv("XIAOMI_BASE_URL"); u != "" {
			return key, u, nil
		}
		return key, XIAOMI_BASE_URL, nil
	case "deepseek":
		key := os.Getenv("DEEPSEEK_API_KEY")
		if key == "" {
			return "", "", fmt.Errorf("subagent: DEEPSEEK_API_KEY not set")
		}
		return key, "https://api.deepseek.com/v1", nil
	default:
		return "", "", fmt.Errorf("subagent: unknown provider %q — no API key configured", provider)
	}
}

// Run executes a text-only task against the configured provider. Errors are
// returned directly; worker simulation exists only in explicitly injected tests.
func (w *RealWorker) Run(ctx context.Context) (*Result, error) {
	start := time.Now()

	systemPrompt := `You are a focused sub-agent. Complete the task below and return ONLY the result.
Do not ask questions, do not explain — just do the work and return the output.
You have only the context supplied in this request. You cannot read or modify files, call tools, or inspect external state.
Do not claim to have executed work requiring those capabilities; report the limitation instead.
Respond with the answer directly.`

	userPrompt := fmt.Sprintf("Goal: %s\n\nContext:\n%s", w.cfg.Goal, w.cfg.Context)

	_, maxTokens, err := providers.ResolveTokenLimits(w.provider.Models(), w.cfg.Model, w.cfg.MaxTokens)
	if err != nil {
		return nil, fmt.Errorf("subagent: token limits: %w", err)
	}

	req := providers.Request{
		Model: w.cfg.Model,
		Messages: []providers.Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		MaxTokens:    maxTokens,
		SystemPrompt: systemPrompt,
	}

	if w.cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, w.cfg.Timeout)
		defer cancel()
	}

	resp, err := w.provider.Complete(ctx, req)
	if err != nil {
		// H-26: Return the actual LLM error instead of silently falling
		// back to the stub worker, which fabricated a false success.
		return nil, fmt.Errorf("subagent: provider complete: %w", err)
	}

	tokensIn := int64(resp.Usage.InputTokens)
	tokensOut := int64(resp.Usage.OutputTokens)

	return &Result{
		TaskIndex:  w.cfg.TaskIndex,
		Status:     "completed",
		Summary:    resp.Content,
		TokensIn:   tokensIn,
		TokensOut:  tokensOut,
		CostUSD:    estimateCostUSD(w.cfg.Model, tokensIn, tokensOut),
		DurationMs: time.Since(start).Milliseconds(),
		ExitReason: "completed",
	}, nil
}

// estimateCostUSD returns a rough cost estimate based on model pricing.
// Prices are conservative approximations for common providers.
func estimateCostUSD(model string, tokensIn, tokensOut int64) float64 {
	// Default pricing: $0.30/1M input, $0.60/1M output (Xiaomi Mimo range).
	inputRate := 0.30 / 1_000_000
	outputRate := 0.60 / 1_000_000

	switch {
	case strings.Contains(strings.ToLower(model), "deepseek"):
		inputRate = 0.27 / 1_000_000
		outputRate = 1.10 / 1_000_000
	case strings.Contains(strings.ToLower(model), "claude"):
		inputRate = 3.00 / 1_000_000
		outputRate = 15.00 / 1_000_000
	case strings.Contains(strings.ToLower(model), "gpt-4"):
		inputRate = 10.00 / 1_000_000
		outputRate = 30.00 / 1_000_000
	case strings.Contains(strings.ToLower(model), "gpt-3.5"):
		inputRate = 0.50 / 1_000_000
		outputRate = 1.50 / 1_000_000
	case strings.Contains(strings.ToLower(model), "gemini"):
		inputRate = 0.15 / 1_000_000
		outputRate = 0.60 / 1_000_000
	}

	return float64(tokensIn)*inputRate + float64(tokensOut)*outputRate
}

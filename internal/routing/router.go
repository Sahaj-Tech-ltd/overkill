package routing

import (
	"context"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

type ComplexityLevel int

const (
	ComplexitySimple ComplexityLevel = iota
	ComplexityModerate
	ComplexityComplex
	ComplexityCritical
)

func (l ComplexityLevel) String() string {
	switch l {
	case ComplexitySimple:
		return "simple"
	case ComplexityModerate:
		return "moderate"
	case ComplexityComplex:
		return "complex"
	case ComplexityCritical:
		return "critical"
	default:
		return "unknown"
	}
}

type ComplexityScore struct {
	Level       ComplexityLevel    `json:"level"`
	Score       float64            `json:"score"`
	Factors     map[string]float64 `json:"factors"`
	Explanation string             `json:"explanation"`
}

type Router interface {
	Route(ctx context.Context, req RouteRequest) (*RouteResult, error)
}

// DefaultEstimatedOutputTokens is the completion assumption for routing estimates.
const DefaultEstimatedOutputTokens = 1024

type RouteRequest struct {
	UserInput      string `json:"user_input"`
	HistoryLength  int    `json:"history_length"`
	ToolCallCount  int    `json:"tool_call_count"`
	HasAttachments bool   `json:"has_attachments"`
	CodeBlockCount int    `json:"code_block_count"`
	// EstimatedTokens is the estimated input token count (legacy field name).
	// Zero infers UserInput text only, without hidden history/media tokens;
	// callers with a full prompt estimate should supply it. Negative values are invalid.
	EstimatedTokens int `json:"estimated_tokens"`
	// EstimatedOutputTokens is planned completion size, not a provider limit.
	// Zero uses DefaultEstimatedOutputTokens; negative estimates are invalid.
	EstimatedOutputTokens int      `json:"estimated_output_tokens,omitempty"`
	RequiredCapabilities  []string `json:"required_capabilities"`
}

type RouteResult struct {
	ModelID    string          `json:"model_id"`
	ModelName  string          `json:"model_name"`
	Provider   string          `json:"provider"`
	Complexity ComplexityScore `json:"complexity"`
	// CostEstimate is USD for this request, not a per-million-token rate.
	// It is meaningful only when CostEstimateKnown is true. Cache, media,
	// tiered pricing, retries and subsequent tool turns are not included.
	CostEstimate          float64 `json:"cost_estimate"`
	CostEstimateKnown     bool    `json:"cost_estimate_known"`
	EstimatedInputTokens  int     `json:"estimated_input_tokens"`
	EstimatedOutputTokens int     `json:"estimated_output_tokens"`
	Reason                string  `json:"reason"`
}

type ProviderModels struct {
	ProviderName string            `json:"provider_name"`
	Models       []providers.Model `json:"models"`
	// PricingKnown preserves explicit zero pricing versus absent metadata.
	// An absent entry infers known only from finite nonzero rates. A false
	// entry marks pricing unknown even when scalar rates contain stale values.
	PricingKnown map[string]bool `json:"pricing_known,omitempty"`
}

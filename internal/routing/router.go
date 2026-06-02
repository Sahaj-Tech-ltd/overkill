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

type RouteRequest struct {
	UserInput            string   `json:"user_input"`
	HistoryLength        int      `json:"history_length"`
	ToolCallCount        int      `json:"tool_call_count"`
	HasAttachments       bool     `json:"has_attachments"`
	CodeBlockCount       int      `json:"code_block_count"`
	EstimatedTokens      int      `json:"estimated_tokens"`
	RequiredCapabilities []string `json:"required_capabilities"`
}

type RouteResult struct {
	ModelID      string          `json:"model_id"`
	ModelName    string          `json:"model_name"`
	Provider     string          `json:"provider"`
	Complexity   ComplexityScore `json:"complexity"`
	CostEstimate float64         `json:"cost_estimate"`
	Reason       string          `json:"reason"`
}

type ProviderModels struct {
	ProviderName string            `json:"provider_name"`
	Models       []providers.Model `json:"models"`
}

package agent

import (
	"fmt"

	"github.com/Sahaj-Tech-ltd/ethos/internal/providers"
	"github.com/Sahaj-Tech-ltd/ethos/internal/tokenizer"
)

type BudgetReport struct {
	SystemPromptTokens int     `json:"system_prompt_tokens"`
	HistoryTokens      int     `json:"history_tokens"`
	ToolDefTokens      int     `json:"tool_def_tokens"`
	EstimatedResponse  int     `json:"estimated_response_tokens"`
	TotalEstimate      int     `json:"total_estimate"`
	MaxTokens          int     `json:"max_tokens"`
	Utilization        float64 `json:"utilization"`
	ShouldCompact      bool    `json:"should_compact"`
	ShouldWarn         bool    `json:"should_warn"`
}

type BudgetEstimator struct {
	estimator               *tokenizer.Estimator
	maxTokens               int
	softThreshold           float64
	hardThreshold           float64
	estimatedResponseTokens int
}

func NewBudgetEstimator(est *tokenizer.Estimator, maxTokens int) *BudgetEstimator {
	return &BudgetEstimator{
		estimator:               est,
		maxTokens:               maxTokens,
		softThreshold:           0.5,
		hardThreshold:           0.95,
		estimatedResponseTokens: 1024,
	}
}

func (be *BudgetEstimator) Estimate(history []providers.Message, systemPrompt string, toolDefs []providers.Tool) *BudgetReport {
	systemTokens := be.estimator.Estimate(systemPrompt)

	historyTokens := 0
	for _, msg := range history {
		historyTokens += be.estimator.Estimate(msg.Content) + 4
	}

	toolTokens := 0
	for _, tool := range toolDefs {
		toolTokens += be.estimator.Estimate(tool.Name+tool.Description+string(tool.Parameters)) + 10
	}

	responseTokens := be.estimatedResponseTokens
	total := systemTokens + historyTokens + toolTokens + responseTokens

	utilization := 0.0
	if be.maxTokens > 0 {
		utilization = float64(total) / float64(be.maxTokens)
	}

	return &BudgetReport{
		SystemPromptTokens: systemTokens,
		HistoryTokens:      historyTokens,
		ToolDefTokens:      toolTokens,
		EstimatedResponse:  responseTokens,
		TotalEstimate:      total,
		MaxTokens:          be.maxTokens,
		Utilization:        utilization,
		ShouldCompact:      utilization >= be.softThreshold,
		ShouldWarn:         utilization >= 0.8,
	}
}

func (be *BudgetEstimator) CheckAndWarn(report *BudgetReport) string {
	if report.Utilization >= be.hardThreshold {
		return fmt.Sprintf("CRITICAL: context utilization %.1f%% exceeds hard threshold %.1f%%. Compaction is required before proceeding.", report.Utilization*100, be.hardThreshold*100)
	}
	if report.ShouldWarn {
		return fmt.Sprintf("WARNING: context utilization %.1f%% is high. Consider compacting conversation history.", report.Utilization*100)
	}
	return ""
}

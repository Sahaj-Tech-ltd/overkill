package agent

import (
	"fmt"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tokenizer"
)

type BudgetReport struct {
	SystemPromptTokens    int     `json:"system_prompt_tokens"`
	HistoryTokens         int     `json:"history_tokens"`
	ToolDefTokens         int     `json:"tool_def_tokens"`
	EstimatedResponse     int     `json:"estimated_response_tokens"`
	IncomingMessageTokens int     `json:"incoming_message_tokens"` // M3: estimated incoming message size
	TotalEstimate         int     `json:"total_estimate"`
	MaxTokens             int     `json:"max_tokens"`
	Utilization           float64 `json:"utilization"`
	ShouldCompact         bool    `json:"should_compact"`
	ShouldWarn            bool    `json:"should_warn"`
	HardExceeded          bool    `json:"hard_exceeded"` // utilization >= hardThreshold
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

// NewBudgetEstimatorWithThresholds allows callers to override the default
// soft/hard trigger percentages (e.g. from config).
func NewBudgetEstimatorWithThresholds(est *tokenizer.Estimator, maxTokens int, softPct, hardPct int) *BudgetEstimator {
	s := 0.5
	h := 0.95
	if softPct > 0 && softPct <= 100 {
		s = float64(softPct) / 100.0
	}
	if hardPct > 0 && hardPct <= 100 {
		h = float64(hardPct) / 100.0
	}
	return &BudgetEstimator{
		estimator:               est,
		maxTokens:               maxTokens,
		softThreshold:           s,
		hardThreshold:           h,
		estimatedResponseTokens: 1024,
	}
}

func (be *BudgetEstimator) Estimate(history []providers.Message, systemPrompt string, toolDefs []providers.Tool) *BudgetReport {
	return be.EstimateWithIncoming(history, systemPrompt, toolDefs, "")
}

// EstimateWithIncoming produces a budget report that includes the estimated
// token cost of an incoming message before it is added to history.
// This allows ShouldCompact to trigger before a large single message
// pushes context from 40% → 95%+ in one turn (M3).
func (be *BudgetEstimator) EstimateWithIncoming(history []providers.Message, systemPrompt string, toolDefs []providers.Tool, incomingMessage string) *BudgetReport {
	systemTokens := be.estimator.Estimate(systemPrompt)

	historyTokens := 0
	for _, msg := range history {
		historyTokens += be.estimator.Estimate(msg.Content) + 4
		// Tool-call assistant messages have their real content in ToolCalls,
		// not Content. Without this, budget underestimates by 2-4x.
		for _, tc := range msg.ToolCalls {
			historyTokens += be.estimator.Estimate(tc.Arguments)
		}
	}

	toolTokens := 0
	for _, tool := range toolDefs {
		toolTokens += be.estimator.Estimate(tool.Name+tool.Description+string(tool.Parameters)) + 10
	}

	incomingTokens := 0
	if incomingMessage != "" {
		incomingTokens = be.estimator.Estimate(incomingMessage) + 4
	}

	responseTokens := be.estimatedResponseTokens
	total := systemTokens + historyTokens + toolTokens + incomingTokens + responseTokens

	utilization := 0.0
	if be.maxTokens > 0 {
		utilization = float64(total) / float64(be.maxTokens)
	}

	return &BudgetReport{
		SystemPromptTokens:    systemTokens,
		HistoryTokens:         historyTokens,
		ToolDefTokens:         toolTokens,
		EstimatedResponse:     responseTokens,
		IncomingMessageTokens: incomingTokens,
		TotalEstimate:         total,
		MaxTokens:             be.maxTokens,
		Utilization:           utilization,
		ShouldCompact:         utilization >= be.softThreshold,
		ShouldWarn:            utilization >= 0.8,
		HardExceeded:          utilization >= be.hardThreshold,
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

package compaction

import (
	"context"
	"fmt"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tokenizer"
)

// AgentCompactor adapts LCMCompactor to the agent.HistoryCompactor interface.
// Constructed via NewAgentCompactor; safe to use as the agent's compactor.
type AgentCompactor struct {
	lcm             *LCMCompactor
	preserveLast    int
	softThreshold   float64
	hardThreshold   float64
	compactionModel string
}

// SetCompactionModel overrides the model used for the summarisation LLM
// call. Empty string restores the DefaultCompactOptions default.
func (c *AgentCompactor) SetCompactionModel(model string) {
	if c == nil {
		return
	}
	c.compactionModel = model
}

// NewAgentCompactor builds an LCM-backed compactor adapter for use with
// Agent.SetCompactor. Defaults match DefaultCompactOptions; pass overrides via
// the optional functional options if needed later.
func NewAgentCompactor(provider providers.Provider, tok *tokenizer.Estimator, preserveLast int) *AgentCompactor {
	if preserveLast <= 0 {
		preserveLast = DefaultCompactOptions().PreserveLast
	}
	return &AgentCompactor{
		lcm:           NewLCMCompactor(provider, tok),
		preserveLast:  preserveLast,
		softThreshold: DefaultCompactOptions().SoftThreshold,
		hardThreshold: DefaultCompactOptions().HardThreshold,
	}
}

// SetAlertSink threads through to the underlying LCMCompactor.
func (c *AgentCompactor) SetAlertSink(s AlertSink, sessionID string) {
	if c == nil || c.lcm == nil {
		return
	}
	c.lcm.SetAlertSink(s, sessionID)
}

// Compact satisfies agent.HistoryCompactor by delegating to the LCM 3-level
// escalation pipeline and returning just the summary string.
func (c *AgentCompactor) Compact(ctx context.Context, msgs []providers.Message, model string, maxTokens int) (string, error) {
	if c == nil || c.lcm == nil {
		return "", fmt.Errorf("compaction: nil compactor")
	}
	opts := DefaultCompactOptions()
	opts.Model = model
	opts.MaxTokens = maxTokens
	opts.PreserveLast = c.preserveLast
	opts.SoftThreshold = c.softThreshold
	opts.HardThreshold = c.hardThreshold
	if c.compactionModel != "" {
		opts.CompactionModel = c.compactionModel
	}

	res, err := c.lcm.Compact(ctx, msgs, opts)
	if err != nil {
		return "", err
	}
	if res == nil {
		return "", fmt.Errorf("compaction: nil result")
	}
	return res.Summary, nil
}

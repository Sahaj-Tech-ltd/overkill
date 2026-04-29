package compaction

import (
	"context"
	"fmt"
	"math"

	"github.com/Sahaj-Tech-ltd/ethos/internal/providers"
	"github.com/Sahaj-Tech-ltd/ethos/internal/tokenizer"
)

type LCMCompactor struct {
	provider  providers.Provider
	tokenizer *tokenizer.Estimator
}

func NewLCMCompactor(provider providers.Provider, tok *tokenizer.Estimator) *LCMCompactor {
	return &LCMCompactor{
		provider:  provider,
		tokenizer: tok,
	}
}

func (c *LCMCompactor) EstimateUsage(messages []providers.Message, model string) int {
	return c.tokenizer.MessageTokenCount(messages)
}

func (c *LCMCompactor) ShouldCompact(messages []providers.Message, opts CompactOptions) (bool, float64) {
	if opts.MaxTokens <= 0 {
		return false, 0
	}
	current := c.EstimateUsage(messages, opts.Model)
	usagePercent := float64(current) / float64(opts.MaxTokens)
	if usagePercent >= opts.HardThreshold {
		return true, usagePercent
	}
	if usagePercent >= opts.SoftThreshold {
		return true, usagePercent
	}
	return false, usagePercent
}

func (c *LCMCompactor) Compact(ctx context.Context, messages []providers.Message, opts CompactOptions) (*CompactionResult, error) {
	if len(messages) == 0 {
		return nil, nil
	}

	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("compaction: %w", err)
	}

	preserveLast := opts.PreserveLast

	splitIdx := len(messages) - preserveLast
	if splitIdx < 0 {
		splitIdx = 0
	}

	toCompact := messages[:splitIdx]
	preserved := messages[splitIdx:]

	if len(toCompact) == 0 {
		return nil, nil
	}

	originalTokens := c.EstimateUsage(toCompact, opts.Model)
	targetTokens := opts.MaxTokens
	if targetTokens <= 0 {
		targetTokens = originalTokens / 2
	}

	result, level, err := c.compactLevels(ctx, toCompact, targetTokens)
	if err != nil {
		return &CompactionResult{
			Summary:           "",
			Level:             level,
			OriginalTokens:    originalTokens,
			CompactedTokens:   0,
			Ratio:             0,
			MessagesCompacted: len(toCompact),
			MessagesPreserved: len(preserved),
			Error:             err.Error(),
		}, err
	}

	compactedTokens := c.tokenizer.Estimate(result)
	ratio := 0.0
	if originalTokens > 0 {
		ratio = float64(compactedTokens) / float64(originalTokens)
	}

	return &CompactionResult{
		Summary:           result,
		Level:             level,
		OriginalTokens:    originalTokens,
		CompactedTokens:   compactedTokens,
		Ratio:             math.Round(ratio*1000) / 1000,
		MessagesCompacted: len(toCompact),
		MessagesPreserved: len(preserved),
	}, nil
}

func (c *LCMCompactor) compactLevels(ctx context.Context, messages []providers.Message, targetTokens int) (string, Level, error) {
	if err := ctx.Err(); err != nil {
		return "", LevelNone, fmt.Errorf("compaction: context cancelled before level 1: %w", err)
	}

	summary, err := c.tryLevel1(ctx, messages, targetTokens)
	if err == nil && c.tokenizer.Estimate(summary) <= targetTokens {
		return summary, Level1Detailed, nil
	}

	if err := ctx.Err(); err != nil {
		return "", LevelNone, fmt.Errorf("compaction: context cancelled before level 2: %w", err)
	}

	summary, err = c.tryLevel2(ctx, messages, targetTokens)
	if err == nil && c.tokenizer.Estimate(summary) <= targetTokens {
		return summary, Level2Aggressive, nil
	}

	return c.level3Truncate(messages, targetTokens), Level3Truncate, nil
}

func (c *LCMCompactor) tryLevel1(ctx context.Context, messages []providers.Message, targetTokens int) (string, error) {
	prompt := buildDetailedSummaryPrompt(messages, targetTokens)
	return c.callLLM(ctx, prompt)
}

func (c *LCMCompactor) tryLevel2(ctx context.Context, messages []providers.Message, targetTokens int) (string, error) {
	prompt := buildAggressiveSummaryPrompt(messages, targetTokens)
	return c.callLLM(ctx, prompt)
}

func (c *LCMCompactor) callLLM(ctx context.Context, prompt string) (string, error) {
	resp, err := c.provider.Complete(ctx, providers.Request{
		Model:    "gpt-4o-mini",
		Messages: []providers.Message{{Role: "user", Content: prompt}},
	})
	if err != nil {
		return "", fmt.Errorf("compaction: LLM call failed: %w", err)
	}
	return resp.Content, nil
}

func (c *LCMCompactor) level3Truncate(messages []providers.Message, targetTokens int) string {
	var allContent string
	for _, msg := range messages {
		allContent += msg.Content + "\n"
	}

	charBudget := targetTokens * 4
	if charBudget < 512 {
		charBudget = 512
	}

	headSize := charBudget / 2
	tailSize := charBudget / 2

	head := allContent
	if len(head) > headSize {
		head = head[:headSize]
	}

	tail := allContent
	if len(tail) > tailSize {
		tail = tail[len(tail)-tailSize:]
	}

	return fmt.Sprintf("[Context summary truncated. Key info: %s\n\nLast known state: %s]", head, tail)
}

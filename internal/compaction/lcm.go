package compaction

import (
	"context"
	"fmt"
	"math"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/tokenizer"
)

// AlertSink receives compaction-skip alerts (master plan §4.19). Tiny
// interface so the compaction package stays free of journal imports.
type AlertSink interface {
	Create(alertType, message, sessionID string) error
}

type LCMCompactor struct {
	provider  providers.Provider
	tokenizer *tokenizer.Estimator
	sink      AlertSink
	sessionID string
}

// SetAlertSink wires a sink that receives a compaction_skip alert when the
// LCM falls all the way to truncation (Level 3) — i.e., no LLM savings.
func (c *LCMCompactor) SetAlertSink(s AlertSink, sessionID string) {
	if c == nil {
		return
	}
	c.sink = s
	c.sessionID = sessionID
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

	result, level, err := c.compactLevels(ctx, toCompact, targetTokens, opts.CompactionModel)
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

	res := &CompactionResult{
		Summary:           result,
		Level:             level,
		OriginalTokens:    originalTokens,
		CompactedTokens:   compactedTokens,
		Ratio:             math.Round(ratio*1000) / 1000,
		MessagesCompacted: len(toCompact),
		MessagesPreserved: len(preserved),
	}
	// Fire compaction_skip alert when LLM levels both failed and we had to
	// fall back to deterministic truncation (no real savings, just slicing).
	if level == Level3Truncate && c.sink != nil {
		func() {
			defer func() { _ = recover() }()
			msg := fmt.Sprintf("compaction fell back to truncate (%d → %d tokens)",
				originalTokens, compactedTokens)
			_ = c.sink.Create("compaction_skip", msg, c.sessionID)
		}()
	}
	return res, nil
}

func (c *LCMCompactor) compactLevels(ctx context.Context, messages []providers.Message, targetTokens int, model string) (string, Level, error) {
	if err := ctx.Err(); err != nil {
		return "", LevelNone, fmt.Errorf("compaction: context cancelled before level 1: %w", err)
	}

	summary, err := c.tryLevel1(ctx, messages, targetTokens, model)
	if err == nil && c.tokenizer.Estimate(summary) <= targetTokens {
		return summary, Level1Detailed, nil
	}

	if err := ctx.Err(); err != nil {
		return "", LevelNone, fmt.Errorf("compaction: context cancelled before level 2: %w", err)
	}

	summary, err = c.tryLevel2(ctx, messages, targetTokens, model)
	if err == nil && c.tokenizer.Estimate(summary) <= targetTokens {
		return summary, Level2Aggressive, nil
	}

	return c.level3Truncate(messages, targetTokens), Level3Truncate, nil
}

func (c *LCMCompactor) tryLevel1(ctx context.Context, messages []providers.Message, targetTokens int, model string) (string, error) {
	prompt := buildDetailedSummaryPrompt(messages, targetTokens)
	return c.callLLM(ctx, prompt, model)
}

func (c *LCMCompactor) tryLevel2(ctx context.Context, messages []providers.Message, targetTokens int, model string) (string, error) {
	prompt := buildAggressiveSummaryPrompt(messages, targetTokens)
	return c.callLLM(ctx, prompt, model)
}

func (c *LCMCompactor) callLLM(ctx context.Context, prompt, model string) (string, error) {
	// Fall back to the historical default if the caller passed an empty
	// model — keeps old direct-call sites working until they migrate.
	if model == "" {
		model = "gpt-4o-mini"
	}
	resp, err := c.provider.Complete(ctx, providers.Request{
		Model:    model,
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

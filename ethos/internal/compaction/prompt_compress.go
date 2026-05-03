package compaction

import (
	"context"
	"fmt"

	"github.com/Sahaj-Tech-ltd/ethos/internal/providers"
)

const compressSystemPrompt = "You are a prompt compressor. Strip filler words, redundancy, and low-salience content while preserving ALL factual content, code, instructions, and specific details. Return ONLY the compressed prompt. Do not add explanations or meta-commentary."

type PromptCompressor struct {
	provider       providers.Provider
	model          string
	enabled        bool
	minSavingRatio float64
}

type CompressionResult struct {
	OriginalTokens   int
	CompressedTokens int
	Ratio            float64
	Skipped          bool
	SkippedReason    string
	ModelUsed        string
}

func NewPromptCompressor(provider providers.Provider, model string) *PromptCompressor {
	return &PromptCompressor{
		provider:       provider,
		model:          model,
		enabled:        true,
		minSavingRatio: 0.3,
	}
}

func (pc *PromptCompressor) Compress(ctx context.Context, prompt string) (string, *CompressionResult, error) {
	result := &CompressionResult{
		ModelUsed: pc.model,
	}

	if !pc.enabled {
		result.Skipped = true
		result.SkippedReason = "disabled"
		result.OriginalTokens = len(prompt) / 4
		return prompt, result, nil
	}

	if pc.provider == nil {
		result.Skipped = true
		result.SkippedReason = "no provider"
		result.OriginalTokens = len(prompt) / 4
		return prompt, result, nil
	}

	originalTokens := len(prompt) / 4
	result.OriginalTokens = originalTokens

	if originalTokens < 500 {
		result.Skipped = true
		result.SkippedReason = "too short to compress"
		return prompt, result, nil
	}

	resp, err := pc.provider.Complete(ctx, providers.Request{
		Model: pc.model,
		Messages: []providers.Message{
			{Role: "system", Content: compressSystemPrompt},
			{Role: "user", Content: prompt},
		},
		MaxTokens: 4096,
	})
	if err != nil {
		result.Skipped = true
		result.SkippedReason = "provider error"
		return prompt, result, fmt.Errorf("prompt compressor: provider error: %w", err)
	}

	compressed := resp.Content
	compressedTokens := len(compressed) / 4
	result.CompressedTokens = compressedTokens

	if originalTokens > 0 {
		result.Ratio = float64(originalTokens-compressedTokens) / float64(originalTokens)
	}

	if result.Ratio < pc.minSavingRatio {
		result.Skipped = true
		result.SkippedReason = fmt.Sprintf("savings ratio %.2f below threshold %.2f", result.Ratio, pc.minSavingRatio)
		return prompt, result, nil
	}

	return compressed, result, nil
}

func (pc *PromptCompressor) SetEnabled(enabled bool) {
	pc.enabled = enabled
}

func (pc *PromptCompressor) SetMinSavingRatio(ratio float64) {
	pc.minSavingRatio = ratio
}

func (pc *PromptCompressor) IsEnabled() bool {
	return pc.enabled
}

// orphan: LCM + prompt-compress middleware (master plan §4.x); awaiting providers-side wiring when cfg.Compaction.PromptCompress is true
package compaction

import (
	"context"

	"github.com/Sahaj-Tech-ltd/ethos/internal/providers"
)

type Level int

const (
	LevelNone       Level = iota
	Level1Detailed        // LLM summary, preserve details
	Level2Aggressive      // LLM summary, bullet points, half target tokens
	Level3Truncate        // Deterministic truncation, no LLM, always succeeds
)

func (l Level) String() string {
	switch l {
	case LevelNone:
		return "none"
	case Level1Detailed:
		return "detailed"
	case Level2Aggressive:
		return "aggressive"
	case Level3Truncate:
		return "truncate"
	default:
		return "unknown"
	}
}

type CompactionResult struct {
	Summary            string  `json:"summary"`
	Level              Level   `json:"level"`
	OriginalTokens     int     `json:"original_tokens"`
	CompactedTokens    int     `json:"compacted_tokens"`
	Ratio              float64 `json:"ratio"`
	MessagesCompacted  int     `json:"messages_compacted"`
	MessagesPreserved  int     `json:"messages_preserved"`
	Error              string  `json:"error,omitempty"`
}

type Compactor interface {
	Compact(ctx context.Context, messages []providers.Message, opts CompactOptions) (*CompactionResult, error)
	EstimateUsage(messages []providers.Message, model string) int
	ShouldCompact(messages []providers.Message, opts CompactOptions) (bool, float64)
}

type CompactOptions struct {
	MaxTokens       int     `json:"max_tokens"`
	PreserveLast    int     `json:"preserve_last"`
	Model           string  `json:"model"`
	CompactionModel string  `json:"compaction_model"`
	SoftThreshold   float64 `json:"soft_threshold"`
	HardThreshold   float64 `json:"hard_threshold"`
}

func DefaultCompactOptions() CompactOptions {
	return CompactOptions{
		PreserveLast:    20,
		SoftThreshold:   0.5,
		HardThreshold:   0.95,
		CompactionModel: "gpt-4o-mini",
	}
}

package tokenizer

import (
	"math"

	"github.com/Sahaj-Tech-ltd/ethos/internal/providers"
)

type Tokenizer interface {
	Count(text string, model string) (int, error)
	Estimate(text string) int
	ContextUsage(text string, maxTokens int) float64
}

type Estimator struct {
	ratios map[string]float64
}

func NewEstimator() *Estimator {
	return &Estimator{
		ratios: map[string]float64{
			"openai":    4.0,
			"anthropic": 3.5,
			"gemini":    4.0,
			"deepseek":  4.0,
			"ollama":    4.0,
			"unknown":   4.0,
		},
	}
}

func (e *Estimator) Count(text string, model string) (int, error) {
	if len(text) == 0 {
		return 0, nil
	}
	family := ModelFamily(model)
	ratio := CharsPerToken(family)
	return int(math.Ceil(float64(len(text)) / ratio)), nil
}

func (e *Estimator) Estimate(text string) int {
	if len(text) == 0 {
		return 0
	}
	return int(math.Ceil(float64(len(text)) / 4.0))
}

func (e *Estimator) ContextUsage(text string, maxTokens int) float64 {
	if maxTokens == 0 {
		return 0.0
	}
	tokens := e.Estimate(text)
	return float64(tokens) / float64(maxTokens)
}

func (e *Estimator) MessageTokenCount(messages []providers.Message) int {
	total := 0
	for _, msg := range messages {
		total += e.Estimate(msg.Content) + 4
	}
	return total
}

package tokenizer

import (
	"math"
	"testing"

	"github.com/Sahaj-Tech-ltd/ethos/internal/providers"
	"github.com/stretchr/testify/assert"
)

func TestEstimate_BasicText(t *testing.T) {
	e := NewEstimator()
	result := e.Estimate("Hello world")
	assert.Equal(t, 3, result)
}

func TestEstimate_EmptyString(t *testing.T) {
	e := NewEstimator()
	result := e.Estimate("")
	assert.Equal(t, 0, result)
}

func TestEstimate_LongText(t *testing.T) {
	e := NewEstimator()
	text := make([]byte, 1000)
	for i := range text {
		text[i] = 'a'
	}
	result := e.Estimate(string(text))
	assert.Equal(t, 250, result)
}

func TestCount_OpenAI(t *testing.T) {
	e := NewEstimator()
	text := "aaaaaaaaaaaaaaaaaaaaaaaa"
	count, err := e.Count(text, "gpt-4o")
	assert.NoError(t, err)
	assert.Equal(t, 6, count)
}

func TestCount_Anthropic(t *testing.T) {
	e := NewEstimator()
	text := "aaaaaaaaaaaaaaaaaaaaaaaa"
	count, err := e.Count(text, "claude-3.5-sonnet")
	assert.NoError(t, err)
	assert.Equal(t, 7, count)
}

func TestCount_Unknown(t *testing.T) {
	e := NewEstimator()
	text := "aaaaaaaaaaaaaaaaaaaaaaaa"
	count, err := e.Count(text, "random-model")
	assert.NoError(t, err)
	assert.Equal(t, 6, count)
}

func TestContextUsage_50Percent(t *testing.T) {
	e := NewEstimator()
	text := make([]byte, 2000)
	for i := range text {
		text[i] = 'a'
	}
	usage := e.ContextUsage(string(text), 1000)
	assert.InDelta(t, 0.5, usage, 0.01)
}

func TestContextUsage_Empty(t *testing.T) {
	e := NewEstimator()
	usage := e.ContextUsage("", 1000)
	assert.Equal(t, 0.0, usage)
}

func TestContextUsage_Over(t *testing.T) {
	e := NewEstimator()
	text := make([]byte, 8000)
	for i := range text {
		text[i] = 'a'
	}
	usage := e.ContextUsage(string(text), 1000)
	assert.True(t, usage > 1.0)
}

func TestMessageTokenCount_Single(t *testing.T) {
	e := NewEstimator()
	msgs := []providers.Message{
		{Role: "user", Content: "aaaaaaaaaaaaaaaa"},
	}
	result := e.MessageTokenCount(msgs)
	assert.Equal(t, 8, result)
}

func TestMessageTokenCount_Multiple(t *testing.T) {
	e := NewEstimator()
	msgs := []providers.Message{
		{Role: "user", Content: "aaaaaaaa"},
		{Role: "assistant", Content: "aaaaaaaa"},
	}
	result := e.MessageTokenCount(msgs)
	assert.Equal(t, 12, result)
}

func TestModelFamily_All(t *testing.T) {
	cases := []struct {
		model    string
		expected string
	}{
		{"gpt-4", "openai"},
		{"gpt-4o", "openai"},
		{"gpt-3.5-turbo", "openai"},
		{"o1-preview", "openai"},
		{"o3-mini", "openai"},
		{"claude-3-opus", "anthropic"},
		{"claude-3.5-sonnet", "anthropic"},
		{"gemini-pro", "gemini"},
		{"gemma-2b", "gemini"},
		{"deepseek-coder", "deepseek"},
		{"llama-3", "ollama"},
		{"mistral-7b", "ollama"},
		{"codellama-13b", "ollama"},
		{"qwen-72b", "ollama"},
	}
	for _, tc := range cases {
		t.Run(tc.model, func(t *testing.T) {
			assert.Equal(t, tc.expected, ModelFamily(tc.model))
		})
	}
}

func TestModelFamily_Unknown(t *testing.T) {
	assert.Equal(t, "unknown", ModelFamily("foo-bar"))
}

func TestCharsPerToken_All(t *testing.T) {
	cases := []struct {
		family   string
		expected float64
	}{
		{"openai", 4.0},
		{"anthropic", 3.5},
		{"gemini", 4.0},
		{"deepseek", 4.0},
		{"ollama", 4.0},
	}
	for _, tc := range cases {
		t.Run(tc.family, func(t *testing.T) {
			assert.Equal(t, tc.expected, CharsPerToken(tc.family))
		})
	}
}

func TestCharsPerToken_Unknown(t *testing.T) {
	assert.Equal(t, 4.0, CharsPerToken("unknown"))
	assert.Equal(t, 4.0, CharsPerToken("anything-else"))
}

func TestContextUsage_ZeroMaxTokens(t *testing.T) {
	e := NewEstimator()
	usage := e.ContextUsage("hello", 0)
	assert.True(t, math.IsNaN(usage) || usage == 0.0)
}

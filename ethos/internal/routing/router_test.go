package routing

import (
	"context"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/stretchr/testify/assert"
)

var testModels = []ProviderModels{
	{
		ProviderName: "openai",
		Models: []providers.Model{
			{ID: "gpt-4o-mini", Name: "GPT-4o Mini", CostIn: 0.15, CostOut: 0.60, SupportsTools: true, SupportsVision: true},
			{ID: "gpt-4o", Name: "GPT-4o", CostIn: 2.50, CostOut: 10.00, SupportsTools: true, SupportsVision: true},
		},
	},
	{
		ProviderName: "anthropic",
		Models: []providers.Model{
			{ID: "claude-3.5-haiku", Name: "Claude 3.5 Haiku", CostIn: 1.00, CostOut: 5.00, SupportsTools: true, SupportsVision: true},
			{ID: "claude-sonnet-4", Name: "Claude Sonnet 4", CostIn: 3.00, CostOut: 15.00, SupportsTools: true, SupportsVision: true},
		},
	},
}

var noVisionModels = []ProviderModels{
	{
		ProviderName: "deepseek",
		Models: []providers.Model{
			{ID: "deepseek-chat", Name: "DeepSeek Chat", CostIn: 0.27, CostOut: 1.10, SupportsTools: true, SupportsVision: false},
		},
	},
	{
		ProviderName: "ollama",
		Models: []providers.Model{
			{ID: "llama3.1:8b", Name: "Llama 3.1 8B", CostIn: 0.00, CostOut: 0.00, SupportsTools: true, SupportsVision: false},
		},
	},
}

func newTestRouter() *SmartRouter {
	classifier := NewClassifier(DefaultThresholds())
	return NewSmartRouter(classifier, testModels, "gpt-4o")
}

func TestClassifier_SimpleInput(t *testing.T) {
	c := NewClassifier(DefaultThresholds())

	tests := []struct {
		name     string
		input    string
		expected ComplexityLevel
	}{
		{"hello", "hello", ComplexitySimple},
		{"what is 2+2", "what is 2+2", ComplexitySimple},
		{"hi there", "hi there", ComplexitySimple},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := c.Classify(RouteRequest{UserInput: tt.input})
			assert.Equal(t, tt.expected, score.Level, "input: %s, score: %.2f", tt.input, score.Score)
		})
	}
}

func TestClassifier_CodeBlocks(t *testing.T) {
	c := NewClassifier(DefaultThresholds())

	t.Run("single code block", func(t *testing.T) {
		score := c.Classify(RouteRequest{UserInput: "fix this code", CodeBlockCount: 1, EstimatedTokens: 500})
		assert.Equal(t, ComplexityModerate, score.Level)
		assert.InDelta(t, 0.20, score.Factors["code_blocks"], 0.001)
	})

	t.Run("two code blocks", func(t *testing.T) {
		score := c.Classify(RouteRequest{UserInput: "fix these", CodeBlockCount: 2, EstimatedTokens: 500})
		assert.Equal(t, ComplexityComplex, score.Level)
		assert.InDelta(t, 0.40, score.Factors["code_blocks"], 0.001)
	})

	t.Run("many code blocks", func(t *testing.T) {
		score := c.Classify(RouteRequest{UserInput: "refactor all", CodeBlockCount: 5})
		assert.Equal(t, ComplexityComplex, score.Level)
	})
}

func TestClassifier_TokenEstimate(t *testing.T) {
	c := NewClassifier(DefaultThresholds())

	t.Run("short input", func(t *testing.T) {
		score := c.Classify(RouteRequest{UserInput: "hi", EstimatedTokens: 50})
		_, hasTokenFactor := score.Factors["token_estimate"]
		assert.False(t, hasTokenFactor)
	})

	t.Run("long input", func(t *testing.T) {
		score := c.Classify(RouteRequest{UserInput: "analyze this large file", EstimatedTokens: 500})
		assert.Equal(t, ComplexityModerate, score.Level)
		assert.InDelta(t, 0.35, score.Factors["token_estimate"], 0.001)
	})
}

func TestClassifier_ToolCalls(t *testing.T) {
	c := NewClassifier(DefaultThresholds())

	t.Run("few tool calls", func(t *testing.T) {
		score := c.Classify(RouteRequest{UserInput: "do something", ToolCallCount: 2})
		_, hasFactor := score.Factors["tool_calls"]
		assert.False(t, hasFactor)
	})

	t.Run("many tool calls", func(t *testing.T) {
		score := c.Classify(RouteRequest{UserInput: "do something", ToolCallCount: 5})
		assert.InDelta(t, 0.25, score.Factors["tool_calls"], 0.001)
		assert.Equal(t, ComplexitySimple, score.Level)
	})

	t.Run("many tool calls with tokens pushes complex", func(t *testing.T) {
		score := c.Classify(RouteRequest{UserInput: "do something", ToolCallCount: 5, EstimatedTokens: 500})
		assert.InDelta(t, 0.25, score.Factors["tool_calls"], 0.001)
		assert.Equal(t, ComplexityComplex, score.Level)
	})
}

func TestClassifier_Attachments(t *testing.T) {
	c := NewClassifier(DefaultThresholds())

	score := c.Classify(RouteRequest{UserInput: "look at this image", HasAttachments: true})
	// Attachments are an additive signal (+0.25), not an absolute
	// override. "describe this image" should not auto-route to the
	// most expensive model — that's a vision-capability requirement
	// handled by the candidate filter, not a complexity tier.
	assert.InDelta(t, 0.25, score.Factors["attachments"], 0.001)
	assert.InDelta(t, 0.25, score.Score, 0.001)
}

func TestClassifier_ConversationDepth(t *testing.T) {
	c := NewClassifier(DefaultThresholds())

	t.Run("shallow conversation", func(t *testing.T) {
		score := c.Classify(RouteRequest{UserInput: "hi", HistoryLength: 5})
		_, hasFactor := score.Factors["conversation_depth"]
		assert.False(t, hasFactor)
	})

	t.Run("deep conversation", func(t *testing.T) {
		score := c.Classify(RouteRequest{UserInput: "continue", HistoryLength: 12})
		assert.InDelta(t, 0.10, score.Factors["conversation_depth"], 0.001)
	})
}

func TestClassifier_Keywords(t *testing.T) {
	c := NewClassifier(DefaultThresholds())

	tests := []struct {
		name  string
		input string
	}{
		{"refactor", "refactor the authentication module"},
		{"architect", "architect a new microservice"},
		{"design", "design the database schema"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := c.Classify(RouteRequest{UserInput: tt.input})
			assert.InDelta(t, 0.20, score.Factors["complex_keywords"], 0.001)
		})
	}
}

func TestClassifier_Explains(t *testing.T) {
	c := NewClassifier(DefaultThresholds())

	t.Run("explain prefix", func(t *testing.T) {
		score := c.Classify(RouteRequest{UserInput: "explain how goroutines work"})
		assert.InDelta(t, -0.10, score.Factors["simple_intent"], 0.001)
	})

	t.Run("what prefix", func(t *testing.T) {
		score := c.Classify(RouteRequest{UserInput: "what is a channel"})
		assert.InDelta(t, -0.10, score.Factors["simple_intent"], 0.001)
	})
}

func TestClassifier_Clamping(t *testing.T) {
	c := NewClassifier(DefaultThresholds())

	t.Run("score never exceeds 1.0", func(t *testing.T) {
		score := c.Classify(RouteRequest{
			UserInput:       "refactor this architecture design",
			EstimatedTokens: 5000,
			CodeBlockCount:  10,
			ToolCallCount:   20,
			HistoryLength:   50,
		})
		assert.LessOrEqual(t, score.Score, 1.0)
		assert.InDelta(t, 1.0, score.Score, 0.001)
	})

	t.Run("score never below 0.0", func(t *testing.T) {
		score := c.Classify(RouteRequest{
			UserInput:      "what is this",
			HasAttachments: false,
		})
		assert.GreaterOrEqual(t, score.Score, 0.0)
	})
}

func TestSmartRouter_RouteSimple(t *testing.T) {
	r := newTestRouter()
	result, err := r.Route(context.Background(), RouteRequest{UserInput: "hello"})
	assert.NoError(t, err)
	assert.Equal(t, "gpt-4o-mini", result.ModelID)
	assert.Equal(t, ComplexitySimple, result.Complexity.Level)
}

func TestSmartRouter_RouteComplex(t *testing.T) {
	r := newTestRouter()
	result, err := r.Route(context.Background(), RouteRequest{
		UserInput:       "refactor the codebase",
		EstimatedTokens: 500,
		CodeBlockCount:  1,
	})
	assert.NoError(t, err)
	assert.Equal(t, ComplexityComplex, result.Complexity.Level)
}

func TestSmartRouter_RouteCritical(t *testing.T) {
	r := newTestRouter()
	// Compose a request that actually meets the Critical threshold:
	// large token count + complex keyword + code block + attachment.
	// Attachments are no longer an absolute override (post-BUG-21
	// fix) — they're additive (+0.25) so they don't single-handedly
	// promote a "describe this image" request to Critical.
	result, err := r.Route(context.Background(), RouteRequest{
		UserInput:       "refactor the entire authentication system across all services",
		EstimatedTokens: 1500,
		CodeBlockCount:  3,
		ToolCallCount:   5,
		HasAttachments:  true,
	})
	assert.NoError(t, err)
	assert.Equal(t, ComplexityCritical, result.Complexity.Level)
	assert.True(t, result.CostEstimate > 0)
}

func TestSmartRouter_CostPriority(t *testing.T) {
	r := newTestRouter()
	r.SetCostPriority(true)

	result, err := r.Route(context.Background(), RouteRequest{
		UserInput:      "refactor this",
		CodeBlockCount: 3,
	})
	assert.NoError(t, err)
	assert.Equal(t, ComplexityComplex, result.Complexity.Level)
}

func TestSmartRouter_DefaultModel(t *testing.T) {
	classifier := NewClassifier(DefaultThresholds())
	r := NewSmartRouter(classifier, []ProviderModels{}, "gpt-4o")

	result, err := r.Route(context.Background(), RouteRequest{UserInput: "hello"})
	assert.NoError(t, err)
	assert.Equal(t, "gpt-4o", result.ModelID)
	assert.Contains(t, result.Reason, "fallback")
}

func TestSmartRouter_Capabilities(t *testing.T) {
	classifier := NewClassifier(DefaultThresholds())
	r := NewSmartRouter(classifier, noVisionModels, "deepseek-chat")

	t.Run("vision required filters out non-vision models", func(t *testing.T) {
		result, err := r.Route(context.Background(), RouteRequest{
			UserInput:            "analyze image",
			HasAttachments:       true,
			RequiredCapabilities: []string{"vision"},
		})
		assert.NoError(t, err)
		assert.Equal(t, "deepseek-chat", result.ModelID)
		assert.Contains(t, result.Reason, "fallback")
	})

	t.Run("tools required keeps tool models", func(t *testing.T) {
		result, err := r.Route(context.Background(), RouteRequest{
			UserInput:            "run tests",
			RequiredCapabilities: []string{"tools"},
		})
		assert.NoError(t, err)
		assert.True(t, result.CostEstimate >= 0)
	})
}

func TestSmartRouter_NoDefaultNoModels_Error(t *testing.T) {
	classifier := NewClassifier(DefaultThresholds())
	r := NewSmartRouter(classifier, []ProviderModels{}, "")

	_, err := r.Route(context.Background(), RouteRequest{UserInput: "hello"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "routing:")
}

func TestModelForComplexity(t *testing.T) {
	r := newTestRouter()

	t.Run("simple returns cheapest", func(t *testing.T) {
		id, _, err := r.ModelForComplexity(ComplexitySimple)
		assert.NoError(t, err)
		assert.Equal(t, "gpt-4o-mini", id)
	})

	t.Run("moderate returns mid-tier", func(t *testing.T) {
		id, _, err := r.ModelForComplexity(ComplexityModerate)
		assert.NoError(t, err)
		assert.Contains(t, []string{"claude-3.5-haiku", "gpt-4o"}, id)
	})

	t.Run("complex returns most expensive", func(t *testing.T) {
		id, _, err := r.ModelForComplexity(ComplexityComplex)
		assert.NoError(t, err)
		assert.Equal(t, "claude-sonnet-4", id)
	})

	t.Run("critical returns most expensive with vision", func(t *testing.T) {
		id, _, err := r.ModelForComplexity(ComplexityCritical)
		assert.NoError(t, err)
		assert.Equal(t, "claude-sonnet-4", id)
	})
}

func TestModelForComplexity_NoVisionModels(t *testing.T) {
	classifier := NewClassifier(DefaultThresholds())
	r := NewSmartRouter(classifier, noVisionModels, "")

	t.Run("critical falls back to most expensive non-vision", func(t *testing.T) {
		id, _, err := r.ModelForComplexity(ComplexityCritical)
		assert.NoError(t, err)
		assert.Equal(t, "deepseek-chat", id)
	})
}

func TestComplexityLevel_String(t *testing.T) {
	tests := []struct {
		level    ComplexityLevel
		expected string
	}{
		{ComplexitySimple, "simple"},
		{ComplexityModerate, "moderate"},
		{ComplexityComplex, "complex"},
		{ComplexityCritical, "critical"},
		{ComplexityLevel(99), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.level.String())
		})
	}
}

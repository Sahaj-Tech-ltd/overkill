package routing

import (
	"math"
	"strings"
)

type ClassifierThresholds struct {
	SimpleModerate  float64 `json:"simple_moderate"`
	ModerateComplex float64 `json:"moderate_complex"`
	ComplexCritical float64 `json:"complex_critical"`
}

func DefaultThresholds() ClassifierThresholds {
	return ClassifierThresholds{
		SimpleModerate:  0.3,
		ModerateComplex: 0.6,
		ComplexCritical: 0.85,
	}
}

type Classifier struct {
	thresholds ClassifierThresholds
}

func NewClassifier(thresholds ClassifierThresholds) *Classifier {
	return &Classifier{thresholds: thresholds}
}

func (c *Classifier) Classify(req RouteRequest) ComplexityScore {
	factors := make(map[string]float64)
	score := 0.0

	if req.EstimatedTokens > 200 {
		factors["token_estimate"] = 0.35
		score += 0.35
	}

	switch {
	case req.CodeBlockCount >= 2:
		factors["code_blocks"] = 0.40
		score += 0.40
	case req.CodeBlockCount == 1:
		factors["code_blocks"] = 0.20
		score += 0.20
	}

	if req.ToolCallCount > 3 {
		factors["tool_calls"] = 0.25
		score += 0.25
	}

	if req.HistoryLength > 10 {
		factors["conversation_depth"] = 0.10
		score += 0.10
	}

	if req.HasAttachments {
		factors["attachments"] = 1.0
		score = 1.0
	}

	lower := strings.ToLower(req.UserInput)
	if strings.HasPrefix(lower, "explain") || strings.HasPrefix(lower, "what") {
		factors["simple_intent"] = -0.10
		score -= 0.10
	}

	if containsComplexKeyword(lower) {
		factors["complex_keywords"] = 0.20
		score += 0.20
	}

	score = math.Max(0.0, math.Min(1.0, score))

	level := c.scoreToLevel(score)

	return ComplexityScore{
		Level:       level,
		Score:       score,
		Factors:     factors,
		Explanation: buildExplanation(level, factors),
	}
}

func (c *Classifier) scoreToLevel(score float64) ComplexityLevel {
	switch {
	case score >= c.thresholds.ComplexCritical:
		return ComplexityCritical
	case score >= c.thresholds.ModerateComplex:
		return ComplexityComplex
	case score >= c.thresholds.SimpleModerate:
		return ComplexityModerate
	default:
		return ComplexitySimple
	}
}

func containsComplexKeyword(input string) bool {
	keywords := []string{"refactor", "architect", "design"}
	for _, kw := range keywords {
		if strings.Contains(input, kw) {
			return true
		}
	}
	return false
}

func buildExplanation(level ComplexityLevel, factors map[string]float64) string {
	return level.String()
}

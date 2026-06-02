package agent

import (
	"strings"
)

type SpecDriver struct {
	enabled             bool
	complexityThreshold float64
}

func NewSpecDriver() *SpecDriver {
	return &SpecDriver{
		enabled:             true,
		complexityThreshold: 0.7,
	}
}

func (sd *SpecDriver) ShouldSpec(userInput string) bool {
	if !sd.enabled {
		return false
	}
	return sd.scoreComplexity(userInput) >= sd.complexityThreshold
}

func (sd *SpecDriver) BuildSpecPrompt(userInput string) string {
	var b strings.Builder
	b.WriteString("Before executing this task, create a structured plan with these sections:\n\n")
	b.WriteString("## Problem\n")
	b.WriteString("[What needs to be solved]\n\n")
	b.WriteString("## Approach\n")
	b.WriteString("[High-level strategy]\n\n")
	b.WriteString("## Files to Modify\n")
	b.WriteString("[List specific files and what changes in each]\n\n")
	b.WriteString("## Test Plan\n")
	b.WriteString("[How to verify the changes work]\n\n")
	b.WriteString("## Edge Cases\n")
	b.WriteString("[What could go wrong]\n\n")
	b.WriteString("After creating the plan, execute it step by step.\n\n")
	b.WriteString(userInput)
	return b.String()
}

func (sd *SpecDriver) IsEnabled() bool {
	return sd.enabled
}

func (sd *SpecDriver) SetEnabled(enabled bool) {
	sd.enabled = enabled
}

func (sd *SpecDriver) SetThreshold(threshold float64) {
	sd.complexityThreshold = threshold
}

func (sd *SpecDriver) scoreComplexity(input string) float64 {
	var score float64

	wordCount := len(strings.Fields(input))
	if wordCount > 50 {
		score += 0.3
	}

	if strings.Contains(input, "```") {
		score += 0.2
	}

	lower := strings.ToLower(input)
	multiStepIndicators := []string{"and then", "also", "after that", "followed by", "refactor", "rewrite", "migrate"}
	for _, indicator := range multiStepIndicators {
		if strings.Contains(lower, indicator) {
			score += 0.2
			break
		}
	}

	archTerms := []string{"module", "system", "architecture", "pipeline", "integration", "end-to-end"}
	for _, term := range archTerms {
		if strings.Contains(lower, term) {
			score += 0.2
			break
		}
	}

	scopeWords := []string{"entire", "all", "every", "complete", "full"}
	for _, word := range scopeWords {
		if strings.Contains(lower, word) {
			score += 0.1
			break
		}
	}

	return score
}

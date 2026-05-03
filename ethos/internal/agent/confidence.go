package agent

import (
	"fmt"
	"strings"

	"github.com/Sahaj-Tech-ltd/ethos/internal/providers"
)

type ConfidenceLevel int

const (
	ConfidenceHigh ConfidenceLevel = iota
	ConfidenceMedium
	ConfidenceLow
	ConfidenceUnknown
)

type ConfidenceAssessment struct {
	Level     ConfidenceLevel
	Score     float64
	Reasoning string
	TaskType  string
}

func (cl ConfidenceLevel) String() string {
	switch cl {
	case ConfidenceHigh:
		return "high"
	case ConfidenceMedium:
		return "medium"
	case ConfidenceLow:
		return "low"
	case ConfidenceUnknown:
		return "unknown"
	default:
		return "unknown"
	}
}

func AssessConfidence(taskType string, history []providers.Message, model string) *ConfidenceAssessment {
	if model == "" {
		return &ConfidenceAssessment{
			Level:     ConfidenceUnknown,
			Score:     0.0,
			Reasoning: "Confidence unknown — can't assess without model info.",
			TaskType:  taskType,
		}
	}

	score := 0.5
	taskLower := strings.ToLower(taskType)

	familiarTaskTypes := []string{"test", "fix", "debug", "explain", "describe", "what"}
	complexTaskTypes := []string{"refactor", "rewrite", "migrate"}

	isFamiliar := false
	for _, ft := range familiarTaskTypes {
		if strings.Contains(taskLower, ft) {
			isFamiliar = true
			break
		}
	}

	isComplex := false
	for _, ct := range complexTaskTypes {
		if strings.Contains(taskLower, ct) {
			isComplex = true
			break
		}
	}

	hasTestTools := false
	for _, msg := range history {
		for _, tc := range msg.ToolCalls {
			tl := strings.ToLower(tc.Name)
			if strings.Contains(tl, "test") || strings.Contains(tl, "pytest") || strings.Contains(tl, "jest") || strings.Contains(tl, "go test") {
				hasTestTools = true
				break
			}
		}
		if hasTestTools {
			break
		}
	}

	if isFamiliar {
		if hasTestTools || !isComplex {
			score += 0.2
		}
	}

	if isComplex {
		score -= 0.1
	}

	if !isFamiliar && !isComplex {
		score -= 0.05
	}

	histLen := len(history)
	if histLen > 10 {
		score += 0.15
	} else if histLen >= 3 {
		score += 0.1
	} else {
		score -= 0.2
	}

	knownModels := []string{"gpt", "claude", "gemini", "llama"}
	modelLower := strings.ToLower(model)
	for _, km := range knownModels {
		if strings.Contains(modelLower, km) {
			score += 0.1
			break
		}
	}

	if score > 1.0 {
		score = 1.0
	}
	if score < 0.0 {
		score = 0.0
	}

	var level ConfidenceLevel
	if score >= 0.7 {
		level = ConfidenceHigh
	} else if score >= 0.4 {
		level = ConfidenceMedium
	} else {
		level = ConfidenceLow
	}

	var reasoning string
	switch {
	case isFamiliar && histLen > 10:
		reasoning = "Familiar task type with rich context."
	case isComplex && histLen >= 3:
		reasoning = "Complex task with moderate context."
	case histLen < 3:
		reasoning = "Limited conversation history for this type of task."
	default:
		reasoning = "Standard task, high confidence."
	}

	return &ConfidenceAssessment{
		Level:     level,
		Score:     score,
		Reasoning: reasoning,
		TaskType:  taskType,
	}
}

func FormatConfidence(ca *ConfidenceAssessment) string {
	if ca.Level == ConfidenceUnknown {
		return "Confidence unknown — can't assess without model info."
	}
	pct := int(ca.Score * 100)
	return fmt.Sprintf("I'm about %d%% confident on this. %s", pct, ca.Reasoning)
}

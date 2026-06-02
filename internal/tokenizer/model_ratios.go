package tokenizer

import "strings"

func ModelFamily(modelID string) string {
	switch {
	case strings.HasPrefix(modelID, "gpt-"),
		strings.HasPrefix(modelID, "o1-"),
		strings.HasPrefix(modelID, "o3-"):
		return "openai"
	case strings.HasPrefix(modelID, "claude-"):
		return "anthropic"
	case strings.HasPrefix(modelID, "gemini-"),
		strings.HasPrefix(modelID, "gemma-"):
		return "gemini"
	case strings.HasPrefix(modelID, "deepseek-"):
		return "deepseek"
	case strings.HasPrefix(modelID, "llama-"),
		strings.HasPrefix(modelID, "mistral-"),
		strings.HasPrefix(modelID, "codellama-"),
		strings.HasPrefix(modelID, "qwen-"):
		return "ollama"
	default:
		return "unknown"
	}
}

func CharsPerToken(modelFamily string) float64 {
	switch modelFamily {
	case "anthropic":
		return 3.5
	case "openai", "gemini", "deepseek", "ollama":
		return 4.0
	default:
		return 4.0
	}
}

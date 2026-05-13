package providers

import (
	"fmt"
)

type FactoryConfig struct {
	Name    string
	Type    string
	APIKey  string
	BaseURL string
	Models  []Model
	Headers map[string]string
}

func NewProvider(cfg FactoryConfig) (Provider, error) {
	switch cfg.Type {
	case "openai":
		models := cfg.Models
		if len(models) == 0 {
			models = OpenAIModels()
		}
		return NewOpenAIProvider(cfg.APIKey, models), nil

	case "anthropic":
		models := cfg.Models
		if len(models) == 0 {
			models = AnthropicModels()
		}
		return NewAnthropicProvider(cfg.APIKey, models), nil

	case "gemini":
		models := cfg.Models
		if len(models) == 0 {
			models = GeminiModels()
		}
		return NewGeminiProvider(cfg.APIKey, models), nil

	case "deepseek":
		models := cfg.Models
		if len(models) == 0 {
			models = DeepSeekModels()
		}
		baseURL := cfg.BaseURL
		if baseURL == "" {
			baseURL = "https://api.deepseek.com/v1"
		}
		return NewOpenAICompatProvider("deepseek", baseURL, cfg.APIKey, models), nil

	case "ollama":
		models := cfg.Models
		if len(models) == 0 {
			models = OllamaModels()
		}
		return NewOllamaProvider(cfg.BaseURL, models), nil

	case "openrouter":
		models := cfg.Models
		if len(models) == 0 {
			models = OpenRouterModels()
		}
		p := NewOpenAICompatProvider("openrouter", "https://openrouter.ai/api/v1", cfg.APIKey, models)
		p.BaseProvider.headers["HTTP-Referer"] = "https://github.com/Sahaj-Tech-ltd/overkill"
		p.BaseProvider.headers["X-Title"] = "Ethos"
		return p, nil

	case "custom":
		if cfg.BaseURL == "" {
			return nil, fmt.Errorf("providers: custom provider requires base_url")
		}
		return NewOpenAICompatProvider(cfg.Name, cfg.BaseURL, cfg.APIKey, cfg.Models), nil

	default:
		return nil, fmt.Errorf("providers: unknown provider type: %s", cfg.Type)
	}
}

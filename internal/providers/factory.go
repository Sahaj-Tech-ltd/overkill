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
		p.BaseProvider.headers["X-Title"] = "Overkill"
		return p, nil

	case "groq":
		models := cfg.Models
		if len(models) == 0 {
			models = GroqModels()
		}
		return NewOpenAICompatProvider("groq", "https://api.groq.com/openai/v1", cfg.APIKey, models), nil

	case "xai":
		models := cfg.Models
		if len(models) == 0 {
			models = XAIModels()
		}
		return NewOpenAICompatProvider("xai", "https://api.x.ai/v1", cfg.APIKey, models), nil

	case "mistral":
		models := cfg.Models
		if len(models) == 0 {
			models = MistralModels()
		}
		return NewOpenAICompatProvider("mistral", "https://api.mistral.ai/v1", cfg.APIKey, models), nil

	case "togetherai":
		models := cfg.Models
		if len(models) == 0 {
			models = TogetherAIModels()
		}
		return NewOpenAICompatProvider("togetherai", "https://api.together.xyz/v1", cfg.APIKey, models), nil

	case "perplexity":
		models := cfg.Models
		if len(models) == 0 {
			models = PerplexityModels()
		}
		return NewOpenAICompatProvider("perplexity", "https://api.perplexity.ai", cfg.APIKey, models), nil

	case "deepinfra":
		return NewOpenAICompatProvider("deepinfra", "https://api.deepinfra.com/v1/openai", cfg.APIKey, cfg.Models), nil

	case "cerebras":
		return NewOpenAICompatProvider("cerebras", "https://api.cerebras.ai/v1", cfg.APIKey, cfg.Models), nil

	case "fireworks":
		return NewOpenAICompatProvider("fireworks", "https://api.fireworks.ai/inference/v1", cfg.APIKey, cfg.Models), nil

	case "bedrock":
		region := cfg.BaseURL
		if region == "" {
			region = "us-east-1"
		}
		// access key + secret read from BaseURL params or env vars.
		// When BaseURL == "us-east-1" it's treated as a region.
		return NewBedrockProvider(region, cfg.APIKey, "", cfg.Models)

	case "custom":
		if cfg.BaseURL == "" {
			return nil, fmt.Errorf("providers: custom provider requires base_url")
		}
		return NewOpenAICompatProvider(cfg.Name, cfg.BaseURL, cfg.APIKey, cfg.Models), nil

	default:
		// Auto-discover from models.dev catalog. If the provider type
		// matches a known catalog entry, use its base URL. This means
		// ANY of the 104+ OpenAI-compatible providers in models.dev
		// can be used without writing Go code — just configure the
		// type and API key.
		if catalogURL := lookupCatalogURL(cfg.Type); catalogURL != "" {
			return NewOpenAICompatProvider(cfg.Name, catalogURL, cfg.APIKey, cfg.Models), nil
		}
		return nil, fmt.Errorf("providers: unknown provider type: %s", cfg.Type)
	}
}

// lookupCatalogURL checks the in-memory models.dev cache for a provider's
// API base URL. Returns empty string if not found or not cached.
func lookupCatalogURL(providerType string) string {
	cat := getGlobalCatalog()
	if cat == nil {
		return ""
	}
	for _, p := range cat.providers {
		if p.ID == providerType && p.API != "" {
			return p.API
		}
	}
	return ""
}

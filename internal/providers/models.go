package providers

func OpenAIModels() []Model {
	return []Model{
		{ID: "gpt-4o", Name: "GPT-4o", MaxTokens: 128000, CostIn: 2.50, CostOut: 10.00, CostCacheIn: 1.25, CostCacheOut: 10.00, SupportsTools: true, SupportsStreaming: true, SupportsVision: true},
		{ID: "gpt-4o-mini", Name: "GPT-4o Mini", MaxTokens: 128000, CostIn: 0.15, CostOut: 0.60, CostCacheIn: 0.075, CostCacheOut: 0.60, SupportsTools: true, SupportsStreaming: true, SupportsVision: true},
		{ID: "o1", Name: "o1", MaxTokens: 128000, CostIn: 15.00, CostOut: 60.00, CostCacheIn: 7.50, CostCacheOut: 60.00, SupportsTools: true, SupportsStreaming: true, SupportsVision: true},
		{ID: "o1-mini", Name: "o1 Mini", MaxTokens: 128000, CostIn: 3.00, CostOut: 12.00, CostCacheIn: 1.50, CostCacheOut: 12.00, SupportsTools: true, SupportsStreaming: true, SupportsVision: false},
		{ID: "o3-mini", Name: "o3 Mini", MaxTokens: 128000, CostIn: 1.10, CostOut: 4.40, CostCacheIn: 0.55, CostCacheOut: 4.40, SupportsTools: true, SupportsStreaming: true, SupportsVision: false},
	}
}

func AnthropicModels() []Model {
	return []Model{
		{ID: "claude-sonnet-4-20250514", Name: "Claude Sonnet 4", MaxTokens: 200000, CostIn: 3.00, CostOut: 15.00, CostCacheIn: 3.00, CostCacheOut: 15.00, SupportsTools: true, SupportsStreaming: true, SupportsVision: true},
		{ID: "claude-3.5-haiku-20241022", Name: "Claude 3.5 Haiku", MaxTokens: 200000, CostIn: 1.00, CostOut: 5.00, CostCacheIn: 1.00, CostCacheOut: 5.00, SupportsTools: true, SupportsStreaming: true, SupportsVision: true},
		{ID: "claude-opus-4-20250514", Name: "Claude Opus 4", MaxTokens: 200000, CostIn: 15.00, CostOut: 75.00, CostCacheIn: 15.00, CostCacheOut: 75.00, SupportsTools: true, SupportsStreaming: true, SupportsVision: true},
	}
}

func GeminiModels() []Model {
	return []Model{
		{ID: "gemini-2.5-pro", Name: "Gemini 2.5 Pro", MaxTokens: 1000000, CostIn: 1.25, CostOut: 5.00, CostCacheIn: 0.3125, CostCacheOut: 5.00, SupportsTools: true, SupportsStreaming: true, SupportsVision: true},
		{ID: "gemini-2.5-flash", Name: "Gemini 2.5 Flash", MaxTokens: 1000000, CostIn: 0.15, CostOut: 0.60, CostCacheIn: 0.0375, CostCacheOut: 0.60, SupportsTools: true, SupportsStreaming: true, SupportsVision: true},
		{ID: "gemini-2.0-flash", Name: "Gemini 2.0 Flash", MaxTokens: 1000000, CostIn: 0.00, CostOut: 0.10, CostCacheIn: 0.00, CostCacheOut: 0.10, SupportsTools: true, SupportsStreaming: true, SupportsVision: true},
	}
}

func DeepSeekModels() []Model {
	return []Model{
		{ID: "deepseek-chat", Name: "DeepSeek Chat", MaxTokens: 128000, CostIn: 0.27, CostOut: 1.10, CostCacheIn: 0.07, CostCacheOut: 1.10, SupportsTools: true, SupportsStreaming: true, SupportsVision: false},
		{ID: "deepseek-reasoner", Name: "DeepSeek Reasoner", MaxTokens: 128000, CostIn: 0.55, CostOut: 2.19, CostCacheIn: 0.14, CostCacheOut: 2.19, SupportsTools: false, SupportsStreaming: true, SupportsVision: false},
	}
}

func OllamaModels() []Model {
	return []Model{
		{ID: "llama3.1:8b", Name: "Llama 3.1 8B", MaxTokens: 128000, CostIn: 0.00, CostOut: 0.00, CostCacheIn: 0.00, CostCacheOut: 0.00, SupportsTools: true, SupportsStreaming: true, SupportsVision: false},
		{ID: "codellama:7b", Name: "Code Llama 7B", MaxTokens: 16384, CostIn: 0.00, CostOut: 0.00, CostCacheIn: 0.00, CostCacheOut: 0.00, SupportsTools: false, SupportsStreaming: true, SupportsVision: false},
		{ID: "mistral:7b", Name: "Mistral 7B", MaxTokens: 32768, CostIn: 0.00, CostOut: 0.00, CostCacheIn: 0.00, CostCacheOut: 0.00, SupportsTools: false, SupportsStreaming: true, SupportsVision: false},
	}
}

func OpenRouterModels() []Model {
	return []Model{
		{ID: "anthropic/claude-sonnet-4-20250514", Name: "Claude Sonnet 4 (via OpenRouter)", MaxTokens: 200000, CostIn: 3.00, CostOut: 15.00, CostCacheIn: 3.00, CostCacheOut: 15.00, SupportsTools: true, SupportsStreaming: true, SupportsVision: true},
		{ID: "openai/gpt-4o", Name: "GPT-4o (via OpenRouter)", MaxTokens: 128000, CostIn: 2.50, CostOut: 10.00, CostCacheIn: 1.25, CostCacheOut: 10.00, SupportsTools: true, SupportsStreaming: true, SupportsVision: true},
		{ID: "google/gemini-2.5-pro", Name: "Gemini 2.5 Pro (via OpenRouter)", MaxTokens: 1000000, CostIn: 1.25, CostOut: 5.00, CostCacheIn: 0.3125, CostCacheOut: 5.00, SupportsTools: true, SupportsStreaming: true, SupportsVision: true},
	}
}

func GroqModels() []Model {
	return []Model{
		{ID: "llama-3.3-70b-versatile", Name: "Llama 3.3 70B Versatile", MaxTokens: 128000, CostIn: 0.59, CostOut: 0.79, CostCacheIn: 0.59, CostCacheOut: 0.79, SupportsTools: true, SupportsStreaming: true, SupportsVision: false},
		{ID: "mixtral-8x7b-32768", Name: "Mixtral 8x7B", MaxTokens: 32768, CostIn: 0.27, CostOut: 0.27, CostCacheIn: 0.27, CostCacheOut: 0.27, SupportsTools: true, SupportsStreaming: true, SupportsVision: false},
		{ID: "gemma2-9b-it", Name: "Gemma 2 9B", MaxTokens: 8192, CostIn: 0.20, CostOut: 0.20, CostCacheIn: 0.20, CostCacheOut: 0.20, SupportsTools: true, SupportsStreaming: true, SupportsVision: false},
	}
}

func XAIModels() []Model {
	return []Model{
		{ID: "grok-2", Name: "Grok 2", MaxTokens: 131072, CostIn: 2.00, CostOut: 8.00, CostCacheIn: 0.00, CostCacheOut: 0.00, SupportsTools: true, SupportsStreaming: true, SupportsVision: false},
	}
}

func MistralModels() []Model {
	return []Model{
		{ID: "mistral-large-latest", Name: "Mistral Large", MaxTokens: 128000, CostIn: 2.00, CostOut: 6.00, CostCacheIn: 2.00, CostCacheOut: 6.00, SupportsTools: true, SupportsStreaming: true, SupportsVision: true},
		{ID: "mistral-medium-latest", Name: "Mistral Medium", MaxTokens: 128000, CostIn: 1.00, CostOut: 3.00, CostCacheIn: 1.00, CostCacheOut: 3.00, SupportsTools: true, SupportsStreaming: true, SupportsVision: false},
		{ID: "mistral-small-latest", Name: "Mistral Small", MaxTokens: 128000, CostIn: 0.20, CostOut: 0.60, CostCacheIn: 0.20, CostCacheOut: 0.60, SupportsTools: true, SupportsStreaming: true, SupportsVision: false},
	}
}

func TogetherAIModels() []Model {
	return []Model{
		{ID: "meta-llama/Llama-3.3-70B-Instruct-Turbo", Name: "Llama 3.3 70B Instruct Turbo", MaxTokens: 131072, CostIn: 0.88, CostOut: 0.88, CostCacheIn: 0.88, CostCacheOut: 0.88, SupportsTools: true, SupportsStreaming: true, SupportsVision: false},
	}
}

// BuiltinModelByID searches every built-in catalog and returns the first
// matching Model by ID. Returns nil when no preset has pricing for the
// given id (e.g. a custom-provider OpenRouter model). Callers that need
// guaranteed coverage should use ModelCatalog.Get() against an on-disk
// models/ directory instead.
func BuiltinModelByID(id string) *Model {
	groups := [][]Model{
		OpenAIModels(), AnthropicModels(), GeminiModels(), DeepSeekModels(),
		OllamaModels(), OpenRouterModels(), GroqModels(), XAIModels(),
		MistralModels(), TogetherAIModels(), PerplexityModels(),
	}
	for _, g := range groups {
		for i := range g {
			if g[i].ID == id {
				out := g[i]
				return &out
			}
		}
	}
	return nil
}

func PerplexityModels() []Model {
	return []Model{
		{ID: "sonar-pro", Name: "Sonar Pro", MaxTokens: 200000, CostIn: 3.00, CostOut: 15.00, CostCacheIn: 0.00, CostCacheOut: 0.00, SupportsTools: true, SupportsStreaming: true, SupportsVision: false},
		{ID: "sonar", Name: "Sonar", MaxTokens: 128000, CostIn: 1.00, CostOut: 1.00, CostCacheIn: 0.00, CostCacheOut: 0.00, SupportsTools: true, SupportsStreaming: true, SupportsVision: false},
	}
}

func DeepInfraModels() []Model {
	return []Model{
		{ID: "meta-llama/Llama-3.3-70B-Instruct-Turbo", Name: "Llama 3.3 70B Instruct Turbo", MaxTokens: 131072, CostIn: 0.59, CostOut: 0.79, SupportsTools: true, SupportsStreaming: true, SupportsVision: false},
		{ID: "meta-llama/Llama-3.1-8B-Instruct", Name: "Llama 3.1 8B Instruct", MaxTokens: 131072, CostIn: 0.06, CostOut: 0.06, SupportsTools: true, SupportsStreaming: true, SupportsVision: false},
	}
}

func CerebrasModels() []Model {
	return []Model{
		{ID: "llama3.1-70b", Name: "Llama 3.1 70B", MaxTokens: 131072, CostIn: 0.60, CostOut: 0.60, SupportsTools: true, SupportsStreaming: true, SupportsVision: false},
		{ID: "llama3.1-8b", Name: "Llama 3.1 8B", MaxTokens: 131072, CostIn: 0.10, CostOut: 0.10, SupportsTools: true, SupportsStreaming: true, SupportsVision: false},
	}
}

func FireworksModels() []Model {
	return []Model{
		{ID: "accounts/fireworks/models/llama-v3p1-70b-instruct", Name: "Llama 3.1 70B Instruct", MaxTokens: 131072, CostIn: 0.90, CostOut: 0.90, SupportsTools: true, SupportsStreaming: true, SupportsVision: false},
		{ID: "accounts/fireworks/models/llama-v3p1-8b-instruct", Name: "Llama 3.1 8B Instruct", MaxTokens: 131072, CostIn: 0.20, CostOut: 0.20, SupportsTools: true, SupportsStreaming: true, SupportsVision: false},
	}
}

func VertexModels() []Model {
	return []Model{
		{ID: "gemini-2.5-flash", Name: "Gemini 2.5 Flash", MaxTokens: 1000000, CostIn: 0.15, CostOut: 0.60, SupportsTools: true, SupportsStreaming: true, SupportsVision: true},
		{ID: "gemini-2.5-pro", Name: "Gemini 2.5 Pro", MaxTokens: 1000000, CostIn: 1.25, CostOut: 5.00, SupportsTools: true, SupportsStreaming: true, SupportsVision: true},
	}
}

func AzureModels() []Model {
	return []Model{
		{ID: "gpt-4o", Name: "GPT-4o", MaxTokens: 128000, CostIn: 2.50, CostOut: 10.00, SupportsTools: true, SupportsStreaming: true, SupportsVision: true},
		{ID: "gpt-4o-mini", Name: "GPT-4o Mini", MaxTokens: 128000, CostIn: 0.15, CostOut: 0.60, SupportsTools: true, SupportsStreaming: true, SupportsVision: true},
	}
}

func CopilotModels() []Model {
	return []Model{
		{ID: "gpt-4o", Name: "GPT-4o (Copilot)", MaxTokens: 128000, CostIn: 0.00, CostOut: 0.00, SupportsTools: true, SupportsStreaming: true, SupportsVision: true},
	}
}

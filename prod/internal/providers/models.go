package providers

func OpenAIModels() []Model {
	return []Model{
		{ID: "gpt-5.2", Name: "GPT-5.2", MaxTokens: 200000, CostIn: 2.50, CostOut: 10.00, SupportsTools: true, SupportsStreaming: true, SupportsVision: true},
		{ID: "gpt-5-pro", Name: "GPT-5 Pro", MaxTokens: 200000, CostIn: 5.00, CostOut: 20.00, SupportsTools: true, SupportsStreaming: true, SupportsVision: true},
		{ID: "gpt-5-mini", Name: "GPT-5 Mini", MaxTokens: 200000, CostIn: 0.50, CostOut: 2.00, SupportsTools: true, SupportsStreaming: true, SupportsVision: true},
		{ID: "gpt-4o", Name: "GPT-4o", MaxTokens: 128000, CostIn: 2.50, CostOut: 10.00, SupportsTools: true, SupportsStreaming: true, SupportsVision: true},
	}
}

func AnthropicModels() []Model {
	return []Model{
		{ID: "claude-opus-4-5", Name: "Claude Opus 4.5", MaxTokens: 200000, CostIn: 15.00, CostOut: 75.00, SupportsTools: true, SupportsStreaming: true, SupportsVision: true},
		{ID: "claude-sonnet-4-6", Name: "Claude Sonnet 4.6", MaxTokens: 200000, CostIn: 3.00, CostOut: 15.00, SupportsTools: true, SupportsStreaming: true, SupportsVision: true},
		{ID: "claude-haiku-4-5", Name: "Claude Haiku 4.5", MaxTokens: 200000, CostIn: 1.00, CostOut: 5.00, SupportsTools: true, SupportsStreaming: true, SupportsVision: true},
		{ID: "claude-opus-4-20250514", Name: "Claude Opus 4", MaxTokens: 200000, CostIn: 15.00, CostOut: 75.00, SupportsTools: true, SupportsStreaming: true, SupportsVision: true},
	}
}

func GeminiModels() []Model {
	return []Model{
		{ID: "gemini-2.5-pro", Name: "Gemini 2.5 Pro", MaxTokens: 1000000, CostIn: 1.25, CostOut: 5.00, SupportsTools: true, SupportsStreaming: true, SupportsVision: true},
		{ID: "gemini-2.5-flash", Name: "Gemini 2.5 Flash", MaxTokens: 1000000, CostIn: 0.15, CostOut: 0.60, SupportsTools: true, SupportsStreaming: true, SupportsVision: true},
		{ID: "gemini-3.1-flash-lite-preview", Name: "Gemini 3.1 Flash Lite Preview", MaxTokens: 1000000, CostIn: 0.10, CostOut: 0.40, SupportsTools: true, SupportsStreaming: true, SupportsVision: true},
		{ID: "gemini-2.0-flash", Name: "Gemini 2.0 Flash", MaxTokens: 1000000, CostIn: 0.00, CostOut: 0.10, SupportsTools: true, SupportsStreaming: true, SupportsVision: true},
	}
}

func DeepSeekModels() []Model {
	return []Model{
		{ID: "deepseek-v4-pro", Name: "DeepSeek V4 Pro", MaxTokens: 128000, CostIn: 0.55, CostOut: 2.19, SupportsTools: true, SupportsStreaming: true, SupportsVision: false},
		{ID: "deepseek-v4-flash", Name: "DeepSeek V4 Flash", MaxTokens: 128000, CostIn: 0.27, CostOut: 1.10, SupportsTools: true, SupportsStreaming: true, SupportsVision: false},
		{ID: "deepseek-chat", Name: "DeepSeek Chat (V3)", MaxTokens: 128000, CostIn: 0.27, CostOut: 1.10, SupportsTools: true, SupportsStreaming: true, SupportsVision: false},
		{ID: "deepseek-reasoner", Name: "DeepSeek Reasoner (R1)", MaxTokens: 128000, CostIn: 0.55, CostOut: 2.19, SupportsTools: false, SupportsStreaming: true, SupportsVision: false},
	}
}

func OllamaModels() []Model {
	return []Model{
		{ID: "llama3.1:8b", Name: "Llama 3.1 8B", MaxTokens: 128000, CostIn: 0.00, CostOut: 0.00, SupportsTools: true, SupportsStreaming: true, SupportsVision: false},
		{ID: "codellama:7b", Name: "Code Llama 7B", MaxTokens: 16384, CostIn: 0.00, CostOut: 0.00, SupportsTools: false, SupportsStreaming: true, SupportsVision: false},
		{ID: "qwen3:14b", Name: "Qwen 3 14B", MaxTokens: 32768, CostIn: 0.00, CostOut: 0.00, SupportsTools: true, SupportsStreaming: true, SupportsVision: false},
	}
}

func OpenRouterModels() []Model {
	return []Model{
		{ID: "anthropic/claude-sonnet-4-6", Name: "Claude Sonnet 4.6", MaxTokens: 200000, CostIn: 3.00, CostOut: 15.00, SupportsTools: true, SupportsStreaming: true, SupportsVision: true},
		{ID: "openai/gpt-5-pro", Name: "GPT-5 Pro", MaxTokens: 200000, CostIn: 5.00, CostOut: 20.00, SupportsTools: true, SupportsStreaming: true, SupportsVision: true},
		{ID: "google/gemini-2.5-pro", Name: "Gemini 2.5 Pro", MaxTokens: 1000000, CostIn: 1.25, CostOut: 5.00, SupportsTools: true, SupportsStreaming: true, SupportsVision: true},
	}
}

func GroqModels() []Model {
	return []Model{
		{ID: "llama-3.3-70b-versatile", Name: "Llama 3.3 70B", MaxTokens: 128000, CostIn: 0.59, CostOut: 0.79, SupportsTools: true, SupportsStreaming: true, SupportsVision: false},
		{ID: "mixtral-8x7b-32768", Name: "Mixtral 8x7B", MaxTokens: 32768, CostIn: 0.27, CostOut: 0.27, SupportsTools: true, SupportsStreaming: true, SupportsVision: false},
		{ID: "qwen-qwq-32b", Name: "Qwen QWQ 32B", MaxTokens: 128000, CostIn: 0.30, CostOut: 0.40, SupportsTools: true, SupportsStreaming: true, SupportsVision: false},
	}
}

func XAIModels() []Model {
	return []Model{
		{ID: "grok-4.3", Name: "Grok 4.3", MaxTokens: 131072, CostIn: 2.00, CostOut: 8.00, SupportsTools: true, SupportsStreaming: true, SupportsVision: false},
		{ID: "grok-4.20-0309-reasoning", Name: "Grok 4.20 Reasoning", MaxTokens: 131072, CostIn: 3.00, CostOut: 12.00, SupportsTools: true, SupportsStreaming: true, SupportsVision: false},
		{ID: "grok-build-0.1", Name: "Grok Build 0.1", MaxTokens: 131072, CostIn: 5.00, CostOut: 15.00, SupportsTools: true, SupportsStreaming: true, SupportsVision: false},
	}
}

func MistralModels() []Model {
	return []Model{
		{ID: "mistral-large-2411", Name: "Mistral Large", MaxTokens: 128000, CostIn: 2.00, CostOut: 6.00, SupportsTools: true, SupportsStreaming: true, SupportsVision: true},
		{ID: "mistral-medium-2508", Name: "Mistral Medium (3.5)", MaxTokens: 128000, CostIn: 1.00, CostOut: 3.00, SupportsTools: true, SupportsStreaming: true, SupportsVision: false},
		{ID: "mistral-small-2603", Name: "Mistral Small (3.2)", MaxTokens: 128000, CostIn: 0.20, CostOut: 0.60, SupportsTools: true, SupportsStreaming: true, SupportsVision: false},
	}
}

func TogetherAIModels() []Model {
	return []Model{
		{ID: "meta-llama/Llama-3.3-70B-Instruct-Turbo", Name: "Llama 3.3 70B", MaxTokens: 131072, CostIn: 0.88, CostOut: 0.88, SupportsTools: true, SupportsStreaming: true, SupportsVision: false},
		{ID: "deepseek-ai/DeepSeek-V4-Pro", Name: "DeepSeek V4 Pro", MaxTokens: 128000, CostIn: 0.55, CostOut: 2.19, SupportsTools: true, SupportsStreaming: true, SupportsVision: false},
	}
}

// BuiltinModelByID searches every built-in catalog and returns the first
// matching Model by ID. Returns nil when no preset has pricing for the
// requested model — the caller should fall back to the generic sentinel.
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
		{ID: "sonar-pro", Name: "Sonar Pro", MaxTokens: 200000, CostIn: 3.00, CostOut: 15.00, SupportsTools: true, SupportsStreaming: true, SupportsVision: false},
		{ID: "sonar", Name: "Sonar", MaxTokens: 128000, CostIn: 1.00, CostOut: 1.00, SupportsTools: true, SupportsStreaming: true, SupportsVision: false},
	}
}

func DeepInfraModels() []Model {
	return []Model{
		{ID: "meta-llama/Llama-3.3-70B-Instruct-Turbo", Name: "Llama 3.3 70B", MaxTokens: 131072, CostIn: 0.59, CostOut: 0.79, SupportsTools: true, SupportsStreaming: true, SupportsVision: false},
		{ID: "meta-llama/Llama-3.1-8B-Instruct", Name: "Llama 3.1 8B", MaxTokens: 131072, CostIn: 0.06, CostOut: 0.06, SupportsTools: true, SupportsStreaming: true, SupportsVision: false},
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
		{ID: "accounts/fireworks/models/llama-v3p1-70b-instruct", Name: "Llama 3.1 70B", MaxTokens: 131072, CostIn: 0.90, CostOut: 0.90, SupportsTools: true, SupportsStreaming: true, SupportsVision: false},
		{ID: "accounts/fireworks/models/llama-v3p1-8b-instruct", Name: "Llama 3.1 8B", MaxTokens: 131072, CostIn: 0.20, CostOut: 0.20, SupportsTools: true, SupportsStreaming: true, SupportsVision: false},
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
	}
}

func CopilotModels() []Model {
	return []Model{
		{ID: "gpt-4o", Name: "GPT-4o (Copilot)", MaxTokens: 128000, CostIn: 0.00, CostOut: 0.00, SupportsTools: true, SupportsStreaming: true, SupportsVision: true},
	}
}

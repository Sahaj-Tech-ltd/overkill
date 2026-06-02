// Package providers — canonical provider default URLs, env vars, and base
// configuration. Centralises URL fragments that were previously duplicated
// across factory.go, fetcher.go, config/setup.go, and config/wizard.go.
package providers

// ProviderDefaults holds the canonical defaults for a provider type.
type ProviderDefaults struct {
	Type      string
	Name      string
	APIKeyEnv string
	BaseURL   string
	Method    ProviderMethod // "native" for non-OpenAI-compat, "openai-compat" otherwise
}

// ProviderMethod describes how the factory constructs the provider.
type ProviderMethod string

const (
	MethodNative       ProviderMethod = "native"
	MethodOpenAICompat ProviderMethod = "openai-compat"
)

// DefaultOllamaURL is the canonical URL for a local Ollama instance.
// Override with the OLLAMA_HOST environment variable when connecting
// to a remote Ollama server.
const DefaultOllamaURL = "http://localhost:11434"

// canonicalDefaults is the single source of truth for every built-in
// provider's default URL, env var, and construction method.
// When adding a new provider, add it HERE and it's automatically available
// in the factory, setup wizard, onboarding catalog, and fetcher.
var canonicalDefaults = []ProviderDefaults{
	{Type: "openai", Name: "OpenAI", APIKeyEnv: "OPENAI_API_KEY", BaseURL: "https://api.openai.com/v1", Method: MethodNative},
	{Type: "anthropic", Name: "Anthropic", APIKeyEnv: "ANTHROPIC_API_KEY", BaseURL: "https://api.anthropic.com", Method: MethodNative},
	{Type: "gemini", Name: "Google Gemini", APIKeyEnv: "GEMINI_API_KEY", BaseURL: "https://generativelanguage.googleapis.com/v1beta", Method: MethodNative},
	{Type: "deepseek", Name: "DeepSeek", APIKeyEnv: "DEEPSEEK_API_KEY", BaseURL: "https://api.deepseek.com/v1", Method: MethodOpenAICompat},
	{Type: "ollama", Name: "Ollama (Local)", APIKeyEnv: "", BaseURL: DefaultOllamaURL, Method: MethodNative},
	{Type: "openrouter", Name: "OpenRouter", APIKeyEnv: "OPENROUTER_API_KEY", BaseURL: "https://openrouter.ai/api/v1", Method: MethodOpenAICompat},
	{Type: "groq", Name: "Groq", APIKeyEnv: "GROQ_API_KEY", BaseURL: "https://api.groq.com/openai/v1", Method: MethodOpenAICompat},
	{Type: "xai", Name: "xAI", APIKeyEnv: "XAI_API_KEY", BaseURL: "https://api.x.ai/v1", Method: MethodOpenAICompat},
	{Type: "mistral", Name: "Mistral", APIKeyEnv: "MISTRAL_API_KEY", BaseURL: "https://api.mistral.ai/v1", Method: MethodOpenAICompat},
	{Type: "togetherai", Name: "Together AI", APIKeyEnv: "TOGETHER_API_KEY", BaseURL: "https://api.together.xyz/v1", Method: MethodOpenAICompat},
	{Type: "perplexity", Name: "Perplexity", APIKeyEnv: "PERPLEXITY_API_KEY", BaseURL: "https://api.perplexity.ai", Method: MethodOpenAICompat},
	{Type: "deepinfra", Name: "DeepInfra", APIKeyEnv: "DEEPINFRA_API_KEY", BaseURL: "https://api.deepinfra.com/v1/openai", Method: MethodOpenAICompat},
	{Type: "cerebras", Name: "Cerebras", APIKeyEnv: "CEREBRAS_API_KEY", BaseURL: "https://api.cerebras.ai/v1", Method: MethodOpenAICompat},
	{Type: "fireworks", Name: "Fireworks AI", APIKeyEnv: "FIREWORKS_API_KEY", BaseURL: "https://api.fireworks.ai/inference/v1", Method: MethodOpenAICompat},
	{Type: "bedrock", Name: "AWS Bedrock", APIKeyEnv: "", BaseURL: "us-east-1", Method: MethodNative},
	{Type: "vertex", Name: "Google Vertex AI", APIKeyEnv: "GOOGLE_APPLICATION_CREDENTIALS", BaseURL: "us-central1-aiplatform.googleapis.com", Method: MethodOpenAICompat},
	{Type: "azure", Name: "Azure OpenAI", APIKeyEnv: "AZURE_OPENAI_API_KEY", BaseURL: "https://{resource}.openai.azure.com/openai", Method: MethodOpenAICompat},
	{Type: "copilot", Name: "GitHub Copilot", APIKeyEnv: "GITHUB_TOKEN", BaseURL: "https://api.githubcopilot.com", Method: MethodOpenAICompat},
}

// CanonicalProviderDefaults returns a copy of the canonical provider defaults.
func CanonicalProviderDefaults() []ProviderDefaults {
	out := make([]ProviderDefaults, len(canonicalDefaults))
	copy(out, canonicalDefaults)
	return out
}

// CanonicalBaseURL returns the canonical base URL for a provider type, or ""
// if unknown.
func CanonicalBaseURL(providerType string) string {
	for _, d := range canonicalDefaults {
		if d.Type == providerType {
			return d.BaseURL
		}
	}
	return ""
}

// CanonicalAPIKeyEnv returns the canonical env var name for a provider type.
func CanonicalAPIKeyEnv(providerType string) string {
	for _, d := range canonicalDefaults {
		if d.Type == providerType {
			return d.APIKeyEnv
		}
	}
	return ""
}

// LookupProviderDefaults returns the full ProviderDefaults for a given type.
func LookupProviderDefaults(providerType string) *ProviderDefaults {
	for _, d := range canonicalDefaults {
		if d.Type == providerType {
			return &d
		}
	}
	return nil
}

// AllProviderTypes returns known provider type names.
func AllProviderTypes() []string {
	types := make([]string, 0, len(canonicalDefaults))
	for _, d := range canonicalDefaults {
		types = append(types, d.Type)
	}
	return types
}

// OpenAICompatBaseURLs returns a map from provider type to base URL, but only
// for providers that use the OpenAI-compatible API style. Used by the
// factory's fallback path and any code that needs to route by provider+URL.
func OpenAICompatBaseURLs() map[string]string {
	m := make(map[string]string)
	for _, d := range canonicalDefaults {
		if d.Method == MethodOpenAICompat || d.Type == "openai" || d.Type == "anthropic" || d.Type == "gemini" || d.Type == "ollama" {
			continue // these have their own native providers
		}
		m[d.Type] = d.BaseURL
	}
	return m
}

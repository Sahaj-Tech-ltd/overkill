package providers

import (
	"fmt"
	"os"
	"strings"
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
	p, err := newProviderRaw(cfg)
	if err != nil {
		return nil, err
	}
	if len(cfg.Headers) > 0 {
		if hs, ok := p.(interface{ SetCustomHeaders(map[string]string) }); ok {
			hs.SetCustomHeaders(cfg.Headers)
		}
	}
	return p, nil
}

func newProviderRaw(cfg FactoryConfig) (Provider, error) {
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
			baseURL = CanonicalBaseURL("deepseek")
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
		p := NewOpenAICompatProvider("openrouter", CanonicalBaseURL("openrouter"), cfg.APIKey, models)
		p.BaseProvider.headers["HTTP-Referer"] = "https://github.com/Sahaj-Tech-ltd/overkill"
		p.BaseProvider.headers["X-Title"] = "Overkill"
		return p, nil

	case "groq":
		models := cfg.Models
		if len(models) == 0 {
			models = GroqModels()
		}
		return NewOpenAICompatProvider("groq", CanonicalBaseURL("groq"), cfg.APIKey, models), nil

	case "xai":
		models := cfg.Models
		if len(models) == 0 {
			models = XAIModels()
		}
		return NewOpenAICompatProvider("xai", CanonicalBaseURL("xai"), cfg.APIKey, models), nil

	case "mistral":
		models := cfg.Models
		if len(models) == 0 {
			models = MistralModels()
		}
		return NewOpenAICompatProvider("mistral", CanonicalBaseURL("mistral"), cfg.APIKey, models), nil

	case "togetherai":
		models := cfg.Models
		if len(models) == 0 {
			models = TogetherAIModels()
		}
		return NewOpenAICompatProvider("togetherai", CanonicalBaseURL("togetherai"), cfg.APIKey, models), nil

	case "perplexity":
		models := cfg.Models
		if len(models) == 0 {
			models = PerplexityModels()
		}
		return NewOpenAICompatProvider("perplexity", CanonicalBaseURL("perplexity"), cfg.APIKey, models), nil

	case "deepinfra":
		models := cfg.Models
		if len(models) == 0 {
			models = DeepInfraModels()
		}
		return NewOpenAICompatProvider("deepinfra", CanonicalBaseURL("deepinfra"), cfg.APIKey, models), nil

	case "cerebras":
		models := cfg.Models
		if len(models) == 0 {
			models = CerebrasModels()
		}
		return NewOpenAICompatProvider("cerebras", CanonicalBaseURL("cerebras"), cfg.APIKey, models), nil

	case "fireworks":
		models := cfg.Models
		if len(models) == 0 {
			models = FireworksModels()
		}
		return NewOpenAICompatProvider("fireworks", CanonicalBaseURL("fireworks"), cfg.APIKey, models), nil

	case "bedrock":
		region := cfg.BaseURL
		if region == "" {
			region = os.Getenv("AWS_REGION")
		}
		if region == "" {
			region = os.Getenv("AWS_DEFAULT_REGION")
		}
		if region == "" {
			region = "us-east-1"
		}
		accessKey := cfg.APIKey
		secretKey := ""
		// AWS needs two separate credentials. If cfg.APIKey is colon-separated
		// "accessKeyID:secretAccessKey", split them. Otherwise fall back to
		// the standard AWS env vars.
		if accessKey != "" {
			if idx := strings.IndexByte(accessKey, ':'); idx > 0 {
				secretKey = accessKey[idx+1:]
				accessKey = accessKey[:idx]
			}
		}
		if accessKey == "" || secretKey == "" {
			return nil, fmt.Errorf("providers: bedrock requires both AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY — set them via the api_key field as \"accessKeyID:secretAccessKey\" or via environment variables")
		}
		return NewBedrockProvider(region, accessKey, secretKey, cfg.Models)

	// Vertex AI — Google Cloud's managed AI platform. Uses GCP service
	// account auth (GOOGLE_APPLICATION_CREDENTIALS env var or gcloud
	// ADC). Reads project from GOOGLE_CLOUD_PROJECT and location from
	// GOOGLE_CLOUD_LOCATION (default: us-central1).
	// Uses the OpenAI-compatible endpoint at:
	//   https://{location}-aiplatform.googleapis.com/v1/projects/{project}/locations/{location}/endpoints/openapi
	case "vertex":
		models := cfg.Models
		if len(models) == 0 {
			models = VertexModels()
		}
		location := os.Getenv("GOOGLE_CLOUD_LOCATION")
		if location == "" {
			location = "us-central1"
		}
		project := os.Getenv("GOOGLE_CLOUD_PROJECT")
		if project == "" {
			return nil, fmt.Errorf("providers: vertex requires GOOGLE_CLOUD_PROJECT env var")
		}
		baseURL := fmt.Sprintf("https://%s-aiplatform.googleapis.com/v1/projects/%s/locations/%s/endpoints/openapi",
			location, project, location)
		apiKey := cfg.APIKey
		if apiKey == "" {
			apiKey = os.Getenv("GOOGLE_API_KEY")
		}
		return NewOpenAICompatProvider("vertex", baseURL, apiKey, models), nil

	// Azure OpenAI — Microsoft's managed OpenAI service. Uses Azure AD
	// token auth or API key. The base URL format is:
	//   https://{resource}.openai.azure.com/openai/v1
	// where {resource} is read from AZURE_OPENAI_RESOURCE env var (required)
	// or from the BaseURL config field. If BaseURL is set explicitly, it
	// is used as-is (supports custom endpoints like API Management).
	case "azure":
		models := cfg.Models
		if len(models) == 0 {
			models = AzureModels()
		}
		baseURL := cfg.BaseURL
		if baseURL == "" {
			resource := os.Getenv("AZURE_OPENAI_RESOURCE")
			if resource == "" {
				return nil, fmt.Errorf("providers: azure requires AZURE_OPENAI_RESOURCE env var or base_url in config")
			}
			baseURL = fmt.Sprintf("https://%s.openai.azure.com/openai/v1", resource)
		}
		apiKey := cfg.APIKey
		if apiKey == "" {
			apiKey = os.Getenv("AZURE_OPENAI_API_KEY")
		}
		return NewOpenAICompatProvider("azure", baseURL, apiKey, models), nil

	case "copilot":
		models := cfg.Models
		if len(models) == 0 {
			models = CopilotModels()
		}
		// GitHub Copilot — uses GitHub device flow OAuth. Set
		// GITHUB_TOKEN or COPILOT_TOKEN in env. Auto-discovers
		// base URL from models.dev.
		return NewOpenAICompatProvider("copilot", CanonicalBaseURL("copilot"), cfg.APIKey, models), nil

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

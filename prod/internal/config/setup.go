package config

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

type SetupStep struct {
	ID       string
	Prompt   string
	Options  []string
	Default  string
	Validate func(string) error
}

type ProviderSetup struct {
	Name         string
	APIKeyEnv    string
	DefaultBase  string
	AltEndpoints []string
	Models       []string
}

type SetupWizard struct {
	config    *Config
	providers map[string]ProviderSetup
	selected  string
}

// providerToCatalog maps setup wizard provider keys to models.dev catalog IDs.
var providerToCatalog = map[string]string{
	"openai":     "openai",
	"anthropic":  "anthropic",
	"gemini":     "google",
	"deepseek":   "deepseek",
	"ollama":     "ollama", // local, no catalog needed
	"openrouter": "openrouter",
	"groq":       "groq",
	"xai":        "xai",
	"mistral":    "mistral",
	"togetherai": "togetherai",
	"perplexity": "perplexity",
	"deepinfra":  "deepinfra",
	"cerebras":   "cerebras",
	"fireworks":  "fireworks-ai",
	"bedrock":    "amazon-bedrock",
	"vertex":     "google-vertex",
	"azure":      "azure",
	"copilot":    "github-copilot",
}

// ProviderToCatalogID returns the models.dev catalog ID for a setup wizard
// provider key, or ("", false) if no mapping exists.
func ProviderToCatalogID(key string) (string, bool) {
	id, ok := providerToCatalog[key]
	return id, ok
}

var builtinProviders = map[string]ProviderSetup{
	"openai": {
		Name:        "OpenAI",
		APIKeyEnv:   providers.CanonicalAPIKeyEnv("openai"),
		DefaultBase: providers.CanonicalBaseURL("openai"),
		Models:      []string{"gpt-4o", "o1", "o1-mini", "o3-mini"},
	},
	"anthropic": {
		Name:        "Anthropic",
		APIKeyEnv:   providers.CanonicalAPIKeyEnv("anthropic"),
		DefaultBase: providers.CanonicalBaseURL("anthropic"),
		Models:      []string{"claude-sonnet-4-20250514", "claude-3.5-haiku-20241022", "claude-opus-4-20250514"},
	},
	"gemini": {
		Name:        "Google Gemini",
		APIKeyEnv:   providers.CanonicalAPIKeyEnv("gemini"),
		DefaultBase: providers.CanonicalBaseURL("gemini"),
		Models:      []string{"gemini-2.5-pro", "gemini-2.5-flash", "gemini-2.0-flash"},
	},
	"deepseek": {
		Name:        "DeepSeek",
		APIKeyEnv:   providers.CanonicalAPIKeyEnv("deepseek"),
		DefaultBase: providers.CanonicalBaseURL("deepseek"),
		Models:      []string{"deepseek-chat", "deepseek-reasoner"},
	},
	"ollama": {
		Name:        "Ollama",
		APIKeyEnv:   providers.CanonicalAPIKeyEnv("ollama"),
		DefaultBase: providers.CanonicalBaseURL("ollama"),
		Models:      []string{"llama3.1:8b", "codellama:7b", "mistral:7b"},
	},
	"openrouter": {
		Name:         "OpenRouter",
		APIKeyEnv:    providers.CanonicalAPIKeyEnv("openrouter"),
		DefaultBase:  providers.CanonicalBaseURL("openrouter"),
		AltEndpoints: []string{providers.CanonicalBaseURL("openrouter")},
		Models:       []string{"anthropic/claude-sonnet-4-20250514", "openai/gpt-4o", "google/gemini-2.5-pro"},
	},
	"groq": {
		Name:        "Groq",
		APIKeyEnv:   providers.CanonicalAPIKeyEnv("groq"),
		DefaultBase: providers.CanonicalBaseURL("groq"),
		Models:      []string{"llama-3.3-70b-versatile", "mixtral-8x7b-32768"},
	},
	"xai": {
		Name:        "xAI",
		APIKeyEnv:   providers.CanonicalAPIKeyEnv("xai"),
		DefaultBase: providers.CanonicalBaseURL("xai"),
		Models:      []string{"grok-2"},
	},
	"mistral": {
		Name:        "Mistral",
		APIKeyEnv:   providers.CanonicalAPIKeyEnv("mistral"),
		DefaultBase: providers.CanonicalBaseURL("mistral"),
		Models:      []string{"mistral-large-latest", "mistral-medium-latest", "mistral-small-latest"},
	},
	"togetherai": {
		Name:        "Together AI",
		APIKeyEnv:   providers.CanonicalAPIKeyEnv("togetherai"),
		DefaultBase: providers.CanonicalBaseURL("togetherai"),
		Models:      []string{"meta-llama/Llama-3.3-70B-Instruct-Turbo"},
	},
	"perplexity": {
		Name:        "Perplexity",
		APIKeyEnv:   providers.CanonicalAPIKeyEnv("perplexity"),
		DefaultBase: providers.CanonicalBaseURL("perplexity"),
		Models:      []string{"sonar-pro", "sonar"},
	},
	"deepinfra": {
		Name:        "DeepInfra",
		APIKeyEnv:   providers.CanonicalAPIKeyEnv("deepinfra"),
		DefaultBase: providers.CanonicalBaseURL("deepinfra"),
		Models:      []string{},
	},
	"cerebras": {
		Name:        "Cerebras",
		APIKeyEnv:   providers.CanonicalAPIKeyEnv("cerebras"),
		DefaultBase: providers.CanonicalBaseURL("cerebras"),
		Models:      []string{},
	},
	"fireworks": {
		Name:        "Fireworks AI",
		APIKeyEnv:   providers.CanonicalAPIKeyEnv("fireworks"),
		DefaultBase: providers.CanonicalBaseURL("fireworks"),
		Models:      []string{},
	},
	"bedrock": {
		Name:        "AWS Bedrock",
		APIKeyEnv:   providers.CanonicalAPIKeyEnv("bedrock"),
		DefaultBase: providers.CanonicalBaseURL("bedrock"),
		Models:      []string{"us.anthropic.claude-sonnet-4-20250514-v1:0"},
	},
	"vertex": {
		Name:        "Google Vertex AI",
		APIKeyEnv:   providers.CanonicalAPIKeyEnv("vertex"),
		DefaultBase: providers.CanonicalBaseURL("vertex"),
		Models:      []string{},
	},
	"azure": {
		Name:        "Azure OpenAI",
		APIKeyEnv:   providers.CanonicalAPIKeyEnv("azure"),
		DefaultBase: providers.CanonicalBaseURL("azure"),
		Models:      []string{},
	},
	"copilot": {
		Name:        "GitHub Copilot",
		APIKeyEnv:   providers.CanonicalAPIKeyEnv("copilot"),
		DefaultBase: providers.CanonicalBaseURL("copilot"),
		Models:      []string{},
	},
}

func NewSetupWizard(cfg *Config) *SetupWizard {
	return &SetupWizard{
		config:    cfg,
		providers: builtinProviders,
	}
}

func (sw *SetupWizard) Steps() []SetupStep {
	return []SetupStep{
		{
			ID:      "provider",
			Prompt:  "Select a provider",
			Options: sw.providerNames(),
			Default: "",
			Validate: func(v string) error {
				return sw.ValidateStep("provider", v)
			},
		},
		{
			ID:      "api_key",
			Prompt:  "Enter API key",
			Default: "",
			Validate: func(v string) error {
				return sw.ValidateStep("api_key", v)
			},
		},
		{
			ID:      "base_url",
			Prompt:  "Enter base URL",
			Default: "",
			Validate: func(v string) error {
				return sw.ValidateStep("base_url", v)
			},
		},
		{
			ID:      "model",
			Prompt:  "Select a model",
			Default: "",
			Validate: func(v string) error {
				return sw.ValidateStep("model", v)
			},
		},
	}
}

func (sw *SetupWizard) ProviderSteps(providerName string) []SetupStep {
	ps, ok := sw.providers[providerName]
	if !ok {
		return nil
	}

	apiKeyValidate := func(v string) error {
		if providerName == "ollama" || providerName == "bedrock" {
			return nil
		}
		return sw.ValidateStep("api_key", v)
	}

	return []SetupStep{
		{
			ID:       "provider",
			Prompt:   "Selected provider",
			Options:  []string{providerName},
			Default:  providerName,
			Validate: func(v string) error { return nil },
		},
		{
			ID:       "api_key",
			Prompt:   fmt.Sprintf("Enter API key (env: %s)", ps.APIKeyEnv),
			Default:  "",
			Validate: apiKeyValidate,
		},
		{
			ID:       "base_url",
			Prompt:   "Enter base URL",
			Default:  ps.DefaultBase,
			Validate: func(v string) error { return sw.ValidateStep("base_url", v) },
		},
		{
			ID:       "model",
			Prompt:   "Select a model",
			Options:  ps.Models,
			Default:  ps.Models[0],
			Validate: func(v string) error { return sw.ValidateStep("model", v) },
		},
	}
}

func (sw *SetupWizard) AvailableProviders() []ProviderSetup {
	names := sw.providerNames()
	result := make([]ProviderSetup, 0, len(names))
	for _, n := range names {
		result = append(result, sw.providers[n])
	}
	return result
}

// AllProviders returns the full provider map (canonical key → ProviderSetup).
func (sw *SetupWizard) AllProviders() map[string]ProviderSetup {
	return sw.providers
}

func (sw *SetupWizard) AvailableProvidersByName() map[string]ProviderSetup {
	names := sw.providerNames()
	result := make(map[string]ProviderSetup, len(names))
	for _, n := range names {
		result[n] = sw.providers[n]
	}
	return result
}

func (sw *SetupWizard) ValidateStep(stepID string, value string) error {
	switch stepID {
	case "provider":
		if _, ok := sw.providers[value]; !ok {
			return fmt.Errorf("unknown provider: %s", value)
		}
		return nil
	case "api_key":
		if sw.selected == "ollama" || sw.selected == "bedrock" {
			return nil
		}
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("api key must not be empty")
		}
		return nil
	case "base_url":
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("base url must not be empty")
		}
		if !strings.HasPrefix(value, "http://") && !strings.HasPrefix(value, "https://") {
			return fmt.Errorf("base url must start with http:// or https://")
		}
		return nil
	case "model":
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("model must not be empty")
		}
		return nil
	default:
		return fmt.Errorf("unknown step: %s", stepID)
	}
}

func (sw *SetupWizard) ApplyStep(stepID string, value string) {
	switch stepID {
	case "provider":
		sw.selected = value
		sw.config.Agent.DefaultProvider = value
		exists := false
		for _, p := range sw.config.Providers {
			if p.Name == value {
				exists = true
				break
			}
		}
		if !exists {
			ps := sw.providers[value]
			sw.config.Providers = append(sw.config.Providers, ProviderConfig{
				Name:    value,
				Type:    value,
				BaseURL: ps.DefaultBase,
				Models:  buildModelConfigs(ps.Models),
			})
		}
	case "api_key":
		if len(sw.config.Providers) > 0 {
			last := &sw.config.Providers[len(sw.config.Providers)-1]
			last.APIKey = value
		}
	case "base_url":
		if len(sw.config.Providers) > 0 {
			last := &sw.config.Providers[len(sw.config.Providers)-1]
			last.BaseURL = value
		}
	case "model":
		sw.config.Agent.DefaultModel = value
	}
}

func (sw *SetupWizard) providerNames() []string {
	names := make([]string, 0, len(sw.providers))
	for k := range sw.providers {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

func buildModelConfigs(ids []string) []ModelConfig {
	models := make([]ModelConfig, 0, len(ids))
	for _, id := range ids {
		models = append(models, ModelConfig{ID: id, Name: id})
	}
	return models
}

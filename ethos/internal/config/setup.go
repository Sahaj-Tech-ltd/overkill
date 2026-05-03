package config

import (
	"fmt"
	"sort"
	"strings"
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

var builtinProviders = map[string]ProviderSetup{
	"openai": {
		Name:        "OpenAI",
		APIKeyEnv:   "OPENAI_API_KEY",
		DefaultBase: "https://api.openai.com/v1",
		Models:      []string{"gpt-4o", "gpt-4o-mini", "o1", "o1-mini", "o3-mini"},
	},
	"anthropic": {
		Name:        "Anthropic",
		APIKeyEnv:   "ANTHROPIC_API_KEY",
		DefaultBase: "https://api.anthropic.com",
		Models:      []string{"claude-sonnet-4-20250514", "claude-3.5-haiku-20241022", "claude-opus-4-20250514"},
	},
	"gemini": {
		Name:        "Google Gemini",
		APIKeyEnv:   "GEMINI_API_KEY",
		DefaultBase: "https://generativelanguage.googleapis.com/v1beta",
		Models:      []string{"gemini-2.5-pro", "gemini-2.5-flash", "gemini-2.0-flash"},
	},
	"deepseek": {
		Name:        "DeepSeek",
		APIKeyEnv:   "DEEPSEEK_API_KEY",
		DefaultBase: "https://api.deepseek.com/v1",
		Models:      []string{"deepseek-chat", "deepseek-reasoner"},
	},
	"ollama": {
		Name:        "Ollama",
		APIKeyEnv:   "",
		DefaultBase: "http://localhost:11434",
		Models:      []string{"llama3.1:8b", "codellama:7b", "mistral:7b"},
	},
	"openrouter": {
		Name:         "OpenRouter",
		APIKeyEnv:    "OPENROUTER_API_KEY",
		DefaultBase:  "https://openrouter.ai/api/v1",
		AltEndpoints: []string{"https://openrouter.ai/api/v1"},
		Models:       []string{"anthropic/claude-sonnet-4-20250514", "openai/gpt-4o", "google/gemini-2.5-pro"},
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
		if providerName == "ollama" {
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
		if sw.selected == "ollama" {
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

package main

import (
	"fmt"
	"os"

	"github.com/Sahaj-Tech-ltd/overkill/internal/childruntime"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/personality"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/subagent"
)

func childWorkerFactory(cfg *config.Config, workspace string) subagent.WorkerFactory {
	factory := childruntime.Factory{Workspace: workspace, Skills: skillRegistry, Resolve: func(provider, model string) (providers.Provider, string, error) {
		return resolveChildProvider(cfg, provider, model)
	}}
	if cfg != nil {
		factory.ReadOnly = cfg.Security.AutonomyLevel == "readonly"
		factory.Instructions = cfg.Agent.Instructions
		factory.Personality = personality.Config{Level: personality.ParseLevel(cfg.Personality.Level), Language: cfg.Personality.Language}
	}
	return func(child subagent.RealWorkerConfig) (subagent.WorkerRunner, error) {
		if child.MaxTokens == 0 && cfg != nil {
			child.MaxTokens = cfg.Agent.MaxOutputTokens
		}
		return factory.Build(child)
	}
}

// resolveChildProvider respects exact configured routes, including custom URL,
// headers, OAuth, and model prices. Failed credentials never select a fallback.
func resolveChildProvider(cfg *config.Config, name, model string) (providers.Provider, string, error) {
	if cfg == nil {
		return nil, "", fmt.Errorf("child runtime: configuration is unavailable")
	}
	configured := append([]config.ProviderConfig(nil), cfg.Providers...)
	if len(configured) == 0 {
		if detected := detectProviderFromEnv(); detected != nil {
			configured = append(configured, *detected)
		}
	}
	if len(configured) == 0 {
		return nil, "", fmt.Errorf("child runtime: no provider configured")
	}
	if name == "" && model == "" {
		name = cfg.Agent.DefaultProvider
		model = cfg.Agent.DefaultModel
	}
	index := -1
	if name != "" {
		for i, provider := range configured {
			if provider.Name == name {
				index = i
				break
			}
		}
		if index < 0 {
			return nil, "", fmt.Errorf("child runtime: provider %q is not configured", name)
		}
	} else if model != "" {
		for i, provider := range configured {
			for _, candidate := range provider.Models {
				if candidate.ID == model {
					if index >= 0 && index != i {
						return nil, "", fmt.Errorf("child runtime: model %q belongs to multiple providers; specify a provider", model)
					}
					index = i
				}
			}
		}
		if index < 0 && len(configured) == 1 {
			index = 0
		}
		if index < 0 {
			return nil, "", fmt.Errorf("child runtime: no configured provider declares model %q", model)
		}
	} else {
		index = 0
	}
	selected := configured[index]
	if model == "" && len(selected.Models) > 0 {
		model = selected.Models[0].ID
	}
	models := config.ProviderModels(selected.Models)
	if len(models) > 0 && model != "" {
		known := false
		for _, entry := range models {
			if entry.ID == model {
				known = true
				break
			}
		}
		if !known {
			return nil, "", fmt.Errorf("child runtime: model %q is not configured for provider %q", model, selected.Name)
		}
	}
	key := selected.APIKey
	if key == "" {
		key = os.Getenv(providerEnvVar(selected.Name))
	}
	if key == "" {
		key = os.Getenv(providers.CanonicalAPIKeyEnv(selected.Type))
	}
	provider, err := providers.NewProvider(providers.FactoryConfig{Name: selected.Name, Type: selected.Type, APIKey: key, BaseURL: selected.BaseURL, Headers: selected.Headers, AuthType: selected.AuthType, Models: models})
	if err != nil {
		return nil, "", fmt.Errorf("child runtime: provider %q: %w", selected.Name, err)
	}
	if model == "" {
		available := provider.Models()
		if len(available) > 0 {
			model = available[0].ID
		}
	}
	if model == "" {
		return nil, "", fmt.Errorf("child runtime: configure a model for provider %q", selected.Name)
	}
	return provider, model, nil
}

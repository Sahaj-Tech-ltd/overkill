package config

import "github.com/Sahaj-Tech-ltd/overkill/internal/providers"

// ProviderModels translates explicit model configuration without ever using
// legacy max_tokens (context capacity) as an output limit.
func ProviderModels(entries []ModelConfig) []providers.Model {
	models := make([]providers.Model, 0, len(entries))
	for _, entry := range entries {
		contextWindow := entry.ContextWindow
		if contextWindow == 0 {
			contextWindow = entry.MaxTokens
		}
		models = append(models, providers.Model{
			ID: entry.ID, Name: entry.Name, MaxTokens: entry.MaxTokens,
			ContextWindow: contextWindow, MaxOutputTokens: entry.MaxOutputTokens, DefaultMaxTokens: entry.MaxOutputTokens,
			CostIn: entry.CostIn, CostOut: entry.CostOut,
			CostCacheIn: entry.CostCacheIn, CostCacheOut: entry.CostCacheOut,
			SupportsTools: true,
		})
	}
	return models
}

// ProviderModels returns the explicitly configured model metadata.
func (p ProviderConfig) ProviderModels() []providers.Model { return ProviderModels(p.Models) }

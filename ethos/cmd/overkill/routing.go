// Package main — wire the live providers catalog into a SmartRouter
// (master plan §5.2). Builds one ProviderModels per catalog entry so the
// router's complexity classifier has the full price/capability matrix to
// choose from. Falls back to nil when the catalog is empty.
package main

import (
	"context"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/routing"
)

// buildSmartRouter pulls the cached/live catalog and constructs a
// SmartRouter rooted at defaultModel. Returns nil when no models are
// available — caller falls back to the static model.
func buildSmartRouter(defaultModel string) *routing.SmartRouter {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cat, err := providers.FetchCatalog(ctx)
	if err != nil || cat == nil {
		return nil
	}
	pms := make([]routing.ProviderModels, 0, 32)
	for _, p := range cat.Providers() {
		flat := make([]providers.Model, 0, len(p.Models))
		for _, m := range cat.Models(p.ID) {
			flat = append(flat, providers.Model{
				ID:               m.ID,
				Name:             m.Name,
				Family:           p.ID,
				ContextWindow:    m.Limit.Context,
				DefaultMaxTokens: m.Limit.Output,
				CostIn:           m.Cost.Input,
				CostOut:          m.Cost.Output,
				CostCacheIn:      m.Cost.CacheRead,
				CostCacheOut:     m.Cost.CacheWrite,
				SupportsTools:    m.ToolCall,
				SupportsVision:   modalitiesContain(m.Modalities.Input, "image"),
				Reasoning:        m.Reasoning,
				Temperature:      m.Temperature,
				Attachment:       m.Attachment,
				OpenWeights:      m.OpenWeights,
				ReleaseDate:      m.ReleaseDate,
				LastUpdated:      m.LastUpdated,
				Knowledge:        m.Knowledge,
			})
		}
		if len(flat) == 0 {
			continue
		}
		pms = append(pms, routing.ProviderModels{ProviderName: p.ID, Models: flat})
	}
	if len(pms) == 0 {
		return nil
	}
	classifier := routing.NewClassifier(routing.DefaultThresholds())
	r := routing.NewSmartRouter(classifier, pms, defaultModel)
	r.SetCostPriority(true)
	return r
}

func modalitiesContain(in []string, want string) bool {
	for _, s := range in {
		if s == want {
			return true
		}
	}
	return false
}

// Package main — wire model catalogs into a SmartRouter (master plan
// §4.2 + §5.2). Source priority:
//
//  1. Local TOML catalog at ~/.overkill/models/ (canonical per §4.2).
//  2. Network catalog from providers.FetchCatalog (models.dev mirror)
//     as fallback when local is empty.
//
// The local catalog also attaches to the router so family-aware and
// capability-aware lookups work — those need the rich Capabilities
// struct that doesn't exist on the network catalog.
package main

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/models"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/routing"
	"github.com/Sahaj-Tech-ltd/overkill/internal/subagent"
)

// buildSmartRouter assembles the router from whichever catalog source
// is available, preferring local TOML. Returns nil when no models can
// be loaded from any source — caller falls back to the static model.
func buildSmartRouter(defaultModel string) *routing.SmartRouter {
	classifier := routing.NewClassifier(routing.DefaultThresholds())

	// 1. Local TOML catalog (§4.2 canonical source).
	if home, err := os.UserHomeDir(); err == nil {
		catRoot := filepath.Join(home, ".overkill", "models")
		if cat, err := models.Load(catRoot); err == nil && cat != nil {
			pms := routing.ProviderModelsFromCatalog(cat)
			if len(pms) > 0 {
				r := routing.NewSmartRouter(classifier, pms, defaultModel)
				r.SetCostPriority(true)
				r.WithCatalog(cat)
				return r
			}
		}
	}

	// 2. Network catalog fallback (models.dev mirror).
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

// taskRouterAdapter wraps *routing.SmartRouter to satisfy subagent.TaskRouter
// so sub-agents get complexity-based, cost-aware model selection.
type taskRouterAdapter struct {
	router *routing.SmartRouter
}

// newTaskRouterAdapter returns a TaskRouter or nil when r is nil.
func newTaskRouterAdapter(r *routing.SmartRouter) subagent.TaskRouter {
	if r == nil {
		return nil
	}
	return &taskRouterAdapter{router: r}
}

func (a *taskRouterAdapter) RouteTask(ctx context.Context, goal, contextStr string) (string, string, bool) {
	userInput := goal
	if contextStr != "" {
		userInput = goal + "\n" + contextStr
	}
	req := routing.RouteRequest{
		UserInput:       userInput,
		EstimatedTokens: (len(goal) + len(contextStr)) * 4 / 3,
	}
	// Sub-agents always need tools — they call tools to do work.
	req.RequiredCapabilities = []string{"tools"}
	res, err := a.router.Route(ctx, req)
	if err != nil || res == nil {
		return "", "", false
	}
	return res.ModelID, res.Provider, true
}

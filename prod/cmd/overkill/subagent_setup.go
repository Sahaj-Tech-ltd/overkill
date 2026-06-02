package main

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/agent"
	"github.com/Sahaj-Tech-ltd/overkill/internal/config"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/Sahaj-Tech-ltd/overkill/internal/routing"
	"github.com/Sahaj-Tech-ltd/overkill/internal/subagent"
)

// subagentInfra bundles the sub-agent runtime wired at boot.
type subagentInfra struct {
	Manager  *subagent.Manager
	Registry *subagent.AgentRegistry
}

// setupSubagentSystem creates the sub-agent manager, registry, and router
// from the loaded config. Called once at boot (tui, web, slack).
func setupSubagentSystem(cfg *config.Config) *subagentInfra {
	cwd, _ := os.Getwd()

	// 1. Build provider model list for the smart router.
	providerModels := buildProviderModels(cfg)

	// 2. Create the smart router with cost priority.
	classifier := routing.NewClassifier(routing.ClassifierThresholds{})
	smartRouter := routing.NewSmartRouter(
		classifier,
		providerModels,
		cfg.Agent.DefaultModel,
	)
	smartRouter.SetCostPriority(true)

	// 3. ModelResolver closure — uses CheapestModel for each agent role.
	resolve := func(h subagent.ModelHint) (string, string) {
		return smartRouter.CheapestModel(h.NeedsVision, h.NeedsTools)
	}

	// 4. Create and populate the agent registry.
	home, _ := os.UserHomeDir()
	projectDir := filepath.Join(cwd, ".overkill", "agents")
	userDir := filepath.Join(home, ".overkill", "agents")
	registry := subagent.NewAgentRegistry(projectDir, userDir)

	// Register built-in agents with router-resolved models.
	for _, def := range subagent.BuiltinAgents(resolve) {
		registry.RegisterBuiltin(def)
	}

	// Scan file-based agents (user + project directories).
	_ = registry.Scan()

	// 5. Create the manager.
	mgrCfg := subagent.Config{
		MaxDepth:         3,
		MaxChildren:      3,
		ChildTimeout:     5 * time.Minute,
		MaxTasksPerChild: 4, // auto-split when parent dumps 5+ tasks
	}
	manager := subagent.NewManager(mgrCfg)

	// Wire router into the manager.
	manager.SetRouter(&routerAdapter{router: smartRouter})

	// Wire registry into the manager.
	manager.SetRegistry(registry)

	return &subagentInfra{
		Manager:  manager,
		Registry: registry,
	}
}

// buildProviderModels converts config providers to routing.ProviderModels.
func buildProviderModels(cfg *config.Config) []routing.ProviderModels {
	if cfg == nil {
		return nil
	}
	var out []routing.ProviderModels
	for _, pc := range cfg.Providers {
		pm := routing.ProviderModels{
			ProviderName: pc.Name,
		}
		for _, mc := range pc.Models {
			pm.Models = append(pm.Models, providers.Model{
				ID:           mc.ID,
				Name:         mc.Name,
				MaxTokens:    mc.MaxTokens,
				CostIn:       mc.CostIn,
				CostOut:      mc.CostOut,
				CostCacheIn:  mc.CostCacheIn,
				CostCacheOut: mc.CostCacheOut,
				// Config doesn't store capabilities — assume common defaults.
				SupportsTools:  true,
				SupportsVision: false,
			})
		}
		out = append(out, pm)
	}
	return out
}

// routerAdapter adapts *routing.SmartRouter to subagent.TaskRouter.
type routerAdapter struct {
	router *routing.SmartRouter
}

func (a *routerAdapter) RouteTask(ctx context.Context, goal, contextStr string) (modelID, provider string, ok bool) {
	if a.router == nil {
		return "", "", false
	}
	req := routing.RouteRequest{
		UserInput: goal,
	}
	result, err := a.router.Route(ctx, req)
	if err != nil || result == nil {
		return "", "", false
	}
	return result.ModelID, result.Provider, true
}

// subagentManagerAdapter adapts *subagent.Manager to agent.SubagentManager.
// Converts subagent.ChildRef to agent.SubagentChild for the agent package.
type subagentManagerAdapter struct {
	mgr *subagent.Manager
}

func (a *subagentManagerAdapter) ActiveCount() int {
	return a.mgr.ActiveCount()
}

func (a *subagentManagerAdapter) ActiveChildren() []agent.SubagentChild {
	refs := a.mgr.ActiveChildren()
	out := make([]agent.SubagentChild, 0, len(refs))
	for _, r := range refs {
		out = append(out, agent.SubagentChild{
			ID:        r.ID,
			Goal:      r.Goal,
			Model:     r.Model,
			Status:    r.Status,
			StartedAt: r.StartedAt.Format(time.RFC3339),
		})
	}
	return out
}

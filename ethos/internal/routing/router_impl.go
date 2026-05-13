package routing

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/Sahaj-Tech-ltd/overkill/internal/models"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

type SmartRouter struct {
	classifier   *Classifier
	providers    []ProviderModels
	defaultModel string
	costPriority bool
	// catalog, when attached via WithCatalog, is consulted by the
	// family-aware + capability-aware lookups (§4.2 + §5.2). Nil
	// means "no catalog, use legacy providers slice only".
	catalog *models.Catalog
}

func NewSmartRouter(classifier *Classifier, providers []ProviderModels, defaultModel string) *SmartRouter {
	return &SmartRouter{
		classifier:   classifier,
		providers:    providers,
		defaultModel: defaultModel,
	}
}

func (r *SmartRouter) SetCostPriority(enabled bool) {
	r.costPriority = enabled
}

func (r *SmartRouter) Route(ctx context.Context, req RouteRequest) (*RouteResult, error) {
	score := r.classifier.Classify(req)

	needsVision := hasCapability(req.RequiredCapabilities, "vision")
	needsTools := hasCapability(req.RequiredCapabilities, "tools")

	candidates := r.collectCandidates(needsVision, needsTools)
	if len(candidates) == 0 {
		if r.defaultModel == "" {
			return nil, fmt.Errorf("routing: no models available for complexity %s", score.Level)
		}
		return r.defaultResult(score), nil
	}

	chosenID, chosenProvider, err := r.ModelForComplexity(score.Level)
	if err != nil {
		if r.defaultModel != "" {
			return r.defaultResult(score), nil
		}
		return nil, fmt.Errorf("routing: %w", err)
	}

	chosenCandidate := findCandidate(candidates, chosenID)
	_ = chosenProvider
	if chosenCandidate == nil {
		if r.defaultModel != "" {
			return r.defaultResult(score), nil
		}
		return nil, fmt.Errorf("routing: selected model %s not found in available providers", chosenID)
	}

	costEstimate := chosenCandidate.model.CostIn + chosenCandidate.model.CostOut

	return &RouteResult{
		ModelID:      chosenCandidate.model.ID,
		ModelName:    chosenCandidate.model.Name,
		Provider:     chosenCandidate.provider,
		Complexity:   score,
		CostEstimate: costEstimate,
		Reason:       fmt.Sprintf("complexity %s (score %.2f) → %s", score.Level, score.Score, chosenCandidate.model.ID),
	}, nil
}

func (r *SmartRouter) ModelForComplexity(level ComplexityLevel) (string, string, error) {
	allModels := r.allSortedModels(false)
	needsVision := level == ComplexityCritical

	switch level {
	case ComplexitySimple, ComplexityModerate, ComplexityComplex:
		if len(allModels) == 0 {
			return "", "", fmt.Errorf("no models available for complexity level %s", level)
		}
		switch level {
		case ComplexitySimple:
			m := allModels[0]
			return m.model.ID, m.provider, nil
		case ComplexityModerate:
			idx := len(allModels) / 2
			m := allModels[idx]
			return m.model.ID, m.provider, nil
		case ComplexityComplex:
			m := allModels[len(allModels)-1]
			return m.model.ID, m.provider, nil
		}
	case ComplexityCritical:
		if needsVision {
			allWithVision := r.allSortedModels(true)
			if len(allWithVision) > 0 {
				return allWithVision[len(allWithVision)-1].model.ID, allWithVision[len(allWithVision)-1].provider, nil
			}
		}
		if len(allModels) > 0 {
			return allModels[len(allModels)-1].model.ID, allModels[len(allModels)-1].provider, nil
		}
		return "", "", fmt.Errorf("no vision-capable model available for critical complexity")
	}

	return "", "", fmt.Errorf("unknown complexity level %d", level)
}

type modelCandidate struct {
	model    providers.Model
	provider string
}

func (r *SmartRouter) collectCandidates(needsVision, needsTools bool) []modelCandidate {
	var candidates []modelCandidate
	for _, pm := range r.providers {
		for _, m := range pm.Models {
			if needsVision && !m.SupportsVision {
				continue
			}
			if needsTools && !m.SupportsTools {
				continue
			}
			candidates = append(candidates, modelCandidate{model: m, provider: pm.ProviderName})
		}
	}
	return candidates
}

func (r *SmartRouter) allSortedModels(needsVision bool) []modelCandidate {
	var candidates []modelCandidate
	for _, pm := range r.providers {
		for _, m := range pm.Models {
			if needsVision && !m.SupportsVision {
				continue
			}
			candidates = append(candidates, modelCandidate{model: m, provider: pm.ProviderName})
		}
	}

	sort.Slice(candidates, func(i, j int) bool {
		ci := candidates[i].model.CostIn + candidates[i].model.CostOut
		cj := candidates[j].model.CostIn + candidates[j].model.CostOut
		return ci < cj
	})

	return candidates
}

func (r *SmartRouter) defaultResult(score ComplexityScore) *RouteResult {
	var modelName string
	var provider string
	for _, pm := range r.providers {
		for _, m := range pm.Models {
			if m.ID == r.defaultModel {
				modelName = m.Name
				provider = pm.ProviderName
				break
			}
		}
	}
	if modelName == "" {
		modelName = r.defaultModel
	}
	return &RouteResult{
		ModelID:      r.defaultModel,
		ModelName:    modelName,
		Provider:     provider,
		Complexity:   score,
		CostEstimate: 0,
		Reason:       fmt.Sprintf("fallback to default model %s (complexity %s)", r.defaultModel, score.Level),
	}
}

func findCandidate(candidates []modelCandidate, modelID string) *modelCandidate {
	for _, c := range candidates {
		if c.model.ID == modelID {
			return &c
		}
	}
	return nil
}

func hasCapability(caps []string, cap string) bool {
	for _, c := range caps {
		if strings.EqualFold(c, cap) {
			return true
		}
	}
	return false
}

package routing

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/Sahaj-Tech-ltd/overkill/internal/models"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

type SmartRouter struct {
	classifier   *Classifier
	providers    []ProviderModels
	defaultModel string
	costPriority atomic.Bool
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

// SetCostPriority switches from legacy complexity tiers to the lowest known
// request estimate, after capability and token-budget filtering. It is safe to
// toggle while routing. This is a cost preference, not a measured quality score.
func (r *SmartRouter) SetCostPriority(enabled bool) {
	r.costPriority.Store(enabled)
}

func (r *SmartRouter) Route(ctx context.Context, req RouteRequest) (*RouteResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for _, capability := range req.RequiredCapabilities {
		if capability != "vision" && capability != "tools" {
			return nil, fmt.Errorf("routing: unsupported required capability %q", capability)
		}
	}
	if req.EstimatedTokens < 0 || req.EstimatedOutputTokens < 0 {
		return nil, fmt.Errorf("routing: estimated token counts must be non-negative")
	}
	inputTokens := req.EstimatedTokens
	if inputTokens == 0 {
		inputTokens = estimateTokens(req.UserInput)
	}
	outputTokens := req.EstimatedOutputTokens
	if outputTokens == 0 {
		outputTokens = DefaultEstimatedOutputTokens
	}
	req.EstimatedTokens = inputTokens
	score := r.classifier.Classify(req)
	needsVision := req.HasAttachments || hasCapability(req.RequiredCapabilities, "vision")
	needsTools := hasCapability(req.RequiredCapabilities, "tools")
	candidates := r.collectCandidates(needsVision, needsTools)
	eligible := candidates[:0]
	for _, candidate := range candidates {
		// A cost preference never bypasses known context/output ceilings. Use
		// the same resolver as execution, including legacy MaxTokens semantics.
		contextWindow, _, err := providers.ResolveTokenLimits([]providers.Model{candidate.model}, candidate.model.ID, outputTokens)
		if err != nil || inputTokens > contextWindow-outputTokens {
			continue
		}
		eligible = append(eligible, candidate)
	}
	if len(eligible) == 0 {
		return nil, fmt.Errorf("routing: no configured models satisfy required capabilities (vision=%t, tools=%t) and token estimates (input=%d, output=%d)", needsVision, needsTools, inputTokens, outputTokens)
	}
	costPriority := r.costPriority.Load()
	chosen, err := selectCandidate(eligible, score.Level, costPriority, inputTokens, outputTokens)
	if err != nil {
		return nil, fmt.Errorf("routing: %w", err)
	}
	costEstimate := 0.0
	if chosen.pricingKnown {
		costEstimate = requestCost(chosen.model, inputTokens, outputTokens)
	}
	reason := fmt.Sprintf("complexity %s (score %.2f) → %s", score.Level, score.Score, chosen.model.ID)
	if costPriority {
		reason += "; cost priority: lowest estimated request cost among eligible models"
	}
	if !chosen.pricingKnown {
		reason += "; price unavailable (not a free-cost estimate)"
	}

	return &RouteResult{
		ModelID:               chosen.model.ID,
		ModelName:             chosen.model.Name,
		Provider:              chosen.provider,
		Complexity:            score,
		CostEstimate:          costEstimate,
		CostEstimateKnown:     chosen.pricingKnown,
		EstimatedInputTokens:  inputTokens,
		EstimatedOutputTokens: outputTokens,
		Reason:                reason,
	}, nil
}

// ModelForComplexity preserves the legacy signature for callers that
// don't know their capability requirements. Internally delegates to
// modelForComplexityWithCaps with no-caps; new code should call the
// capability-aware form directly. Without a request token mix, this method
// ranks equal input/output volumes; Route uses its explicit token assumptions.
func (r *SmartRouter) ModelForComplexity(level ComplexityLevel) (string, string, error) {
	id, provider, err := r.modelForComplexityWithCaps(level, level == ComplexityCritical, false)
	// This legacy API expresses a complexity preference, not required vision.
	// Route uses the strict helper with the request's actual requirements.
	if err != nil && level == ComplexityCritical {
		return r.modelForComplexityWithCaps(level, false, false)
	}
	return id, provider, err
}

func (r *SmartRouter) modelForComplexityWithCaps(level ComplexityLevel, needsVision, needsTools bool) (string, string, error) {
	chosen, err := selectCandidate(r.collectCandidates(needsVision, needsTools), level, r.costPriority.Load(), 1, 1)
	if err != nil {
		return "", "", err
	}
	return chosen.model.ID, chosen.provider, nil
}

func requestCost(model providers.Model, inputTokens, outputTokens int) float64 {
	// Divide before multiplying to avoid intermediate overflow for finite rates.
	return float64(inputTokens)/1_000_000*model.CostIn + float64(outputTokens)/1_000_000*model.CostOut
}

func selectCandidate(candidates []modelCandidate, level ComplexityLevel, costPriority bool, inputTokens, outputTokens int) (modelCandidate, error) {
	if level < ComplexitySimple || level > ComplexityCritical {
		return modelCandidate{}, fmt.Errorf("unknown complexity level %d", level)
	}
	eligible := make([]modelCandidate, 0, len(candidates))
	knownAvailable := false
	for _, c := range candidates {
		if c.pricingKnown && !math.IsInf(requestCost(c.model, inputTokens, outputTokens), 0) {
			knownAvailable = true
		}
	}
	for _, c := range candidates {
		// Preserve unpriced custom-model fallback in complexity mode, but never
		// rank absent prices as cheaper or more capable than priced candidates.
		if (costPriority || knownAvailable) && !c.pricingKnown {
			continue
		}
		if c.pricingKnown && math.IsInf(requestCost(c.model, inputTokens, outputTokens), 0) {
			continue
		}
		eligible = append(eligible, c)
	}
	if len(eligible) == 0 {
		return modelCandidate{}, fmt.Errorf("no eligible models with known valid pricing for complexity %s", level)
	}
	sort.SliceStable(eligible, func(i, j int) bool {
		if costPriority {
			return requestCost(eligible[i].model, inputTokens, outputTokens) < requestCost(eligible[j].model, inputTokens, outputTokens)
		}
		return eligible[i].model.CostIn+eligible[i].model.CostOut < eligible[j].model.CostIn+eligible[j].model.CostOut
	})
	if costPriority || level == ComplexitySimple {
		return eligible[0], nil
	}
	if level == ComplexityModerate {
		return eligible[len(eligible)/2], nil
	}
	return eligible[len(eligible)-1], nil
}

func validModelPrice(m providers.Model) bool {
	return m.CostIn >= 0 && m.CostOut >= 0 && !math.IsNaN(m.CostIn) && !math.IsNaN(m.CostOut) && !math.IsInf(m.CostIn, 0) && !math.IsInf(m.CostOut, 0)
}

type modelCandidate struct {
	model        providers.Model
	provider     string
	pricingKnown bool
}

func (r *SmartRouter) collectCandidates(needsVision, needsTools bool) []modelCandidate {
	var candidates []modelCandidate
	for _, pm := range r.providers {
		if strings.TrimSpace(pm.ProviderName) == "" {
			continue
		}
		for _, m := range pm.Models {
			if strings.TrimSpace(m.ID) == "" {
				continue
			}
			if needsVision && !m.SupportsVision {
				continue
			}
			if needsTools && !m.SupportsTools {
				continue
			}
			if !validModelPrice(m) {
				continue
			}
			known, explicit := pm.PricingKnown[m.ID]
			if !explicit {
				known = m.CostIn > 0 || m.CostOut > 0
			}
			candidates = append(candidates, modelCandidate{model: m, provider: pm.ProviderName, pricingKnown: known})
		}
	}
	return candidates
}

// CheapestModel returns the single cheapest model matching the given
// capability requirements. Used by the built-in agent system to assign
// cost-appropriate models: Explore/Plan get the cheapest reader, Verify
// gets the cheapest tool-capable model, General-purpose inherits. With no
// request mix this compares equal input/output volumes and requires known prices.
func (r *SmartRouter) CheapestModel(needsVision, needsTools bool) (modelID, provider string) {
	candidates := r.collectCandidates(needsVision, needsTools)
	if len(candidates) == 0 {
		return "", ""
	}
	chosen, err := selectCandidate(candidates, ComplexitySimple, true, 1, 1)
	if err != nil {
		return "", ""
	}
	return chosen.model.ID, chosen.provider
}

func hasCapability(caps []string, cap string) bool {
	for _, c := range caps {
		if strings.EqualFold(c, cap) {
			return true
		}
	}
	return false
}

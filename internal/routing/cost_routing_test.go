package routing

import (
	"context"
	"math"
	"strings"
	"sync"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/models"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/stretchr/testify/require"
)

func TestCostPriorityChangesProviderAndCanBeDisabled(t *testing.T) {
	r := newTestRouter()
	req := RouteRequest{UserInput: "refactor this", CodeBlockCount: 3, EstimatedTokens: 100, EstimatedOutputTokens: 200, RequiredCapabilities: []string{"tools"}}
	expensive, err := r.Route(t.Context(), req)
	require.NoError(t, err)
	require.Equal(t, "anthropic", expensive.Provider)
	require.Equal(t, "claude-sonnet-4", expensive.ModelID)
	r.SetCostPriority(true)
	cheap, err := r.Route(t.Context(), req)
	require.NoError(t, err)
	require.Equal(t, "openai", cheap.Provider)
	require.Equal(t, "gpt-4o-mini", cheap.ModelID)
	require.Equal(t, expensive.Complexity.Level, cheap.Complexity.Level)
	require.Less(t, cheap.CostEstimate, expensive.CostEstimate)
	r.SetCostPriority(false)
	restored, err := r.Route(t.Context(), req)
	require.NoError(t, err)
	require.Equal(t, expensive.Provider, restored.Provider)
	require.Equal(t, expensive.ModelID, restored.ModelID)
}

func TestCostPriorityUsesRequestInputOutputMix(t *testing.T) {
	r := NewSmartRouter(NewClassifier(DefaultThresholds()), []ProviderModels{
		{ProviderName: "cheap-input", Models: []providers.Model{{ID: "shared", ContextWindow: 20000, CostIn: 1, CostOut: 10}}},
		{ProviderName: "cheap-output", Models: []providers.Model{{ID: "shared", ContextWindow: 20000, CostIn: 5, CostOut: 1}}},
	}, "")
	r.SetCostPriority(true)
	a, err := r.Route(t.Context(), RouteRequest{EstimatedTokens: 10000, EstimatedOutputTokens: 100})
	require.NoError(t, err)
	require.Equal(t, "cheap-input", a.Provider)
	require.InDelta(t, .011, a.CostEstimate, 1e-12)
	b, err := r.Route(t.Context(), RouteRequest{EstimatedTokens: 100, EstimatedOutputTokens: 10000})
	require.NoError(t, err)
	require.Equal(t, "cheap-output", b.Provider)
	require.InDelta(t, .0105, b.CostEstimate, 1e-12)
}

func TestRouteCostEstimateUnitsScalingAndAssumptions(t *testing.T) {
	r := NewSmartRouter(NewClassifier(DefaultThresholds()), []ProviderModels{{ProviderName: "p", Models: []providers.Model{{ID: "m", CostIn: 2, CostOut: 8}}}}, "")
	small, err := r.Route(t.Context(), RouteRequest{EstimatedTokens: 100, EstimatedOutputTokens: 50})
	require.NoError(t, err)
	require.True(t, small.CostEstimateKnown)
	require.Equal(t, 100, small.EstimatedInputTokens)
	require.Equal(t, 50, small.EstimatedOutputTokens)
	require.InDelta(t, .0006, small.CostEstimate, 1e-15)
	large, err := r.Route(t.Context(), RouteRequest{EstimatedTokens: 1000, EstimatedOutputTokens: 500})
	require.NoError(t, err)
	require.InDelta(t, small.CostEstimate*10, large.CostEstimate, 1e-15)
	inferred, err := r.Route(t.Context(), RouteRequest{UserInput: strings.Repeat("word ", 30)})
	require.NoError(t, err)
	require.Equal(t, 40, inferred.EstimatedInputTokens)
	require.Equal(t, DefaultEstimatedOutputTokens, inferred.EstimatedOutputTokens)
	require.InDelta(t, (40.*2+1024.*8)/1e6, inferred.CostEstimate, 1e-15)
	for _, req := range []RouteRequest{{EstimatedTokens: -1}, {EstimatedOutputTokens: -1}} {
		got, err := r.Route(t.Context(), req)
		require.ErrorContains(t, err, "non-negative")
		require.Nil(t, got)
	}
}

func TestCostPriorityUnknownFreeInvalidAndHardSuitability(t *testing.T) {
	r := NewSmartRouter(NewClassifier(DefaultThresholds()), []ProviderModels{
		{ProviderName: "unknown", Models: []providers.Model{{ID: "unknown", SupportsVision: true, SupportsTools: true}}},
		{ProviderName: "incompatible-free", PricingKnown: map[string]bool{"free": true}, Models: []providers.Model{{ID: "free"}}},
		{ProviderName: "too-small-free", PricingKnown: map[string]bool{"small": true}, Models: []providers.Model{{ID: "small", ContextWindow: 150, MaxOutputTokens: 30, SupportsVision: true, SupportsTools: true}}},
		{ProviderName: "priced", Models: []providers.Model{{ID: "paid", CostIn: 2, CostOut: 3, ContextWindow: 1000, MaxOutputTokens: 100, SupportsVision: true, SupportsTools: true}}},
	}, "")
	r.SetCostPriority(true)
	req := RouteRequest{EstimatedTokens: 200, EstimatedOutputTokens: 50, RequiredCapabilities: []string{"tools", "vision"}}
	got, err := r.Route(t.Context(), req)
	require.NoError(t, err)
	require.Equal(t, "priced", got.Provider)
	require.True(t, got.CostEstimateKnown)
	req.EstimatedOutputTokens = 101
	got, err = r.Route(t.Context(), req)
	require.Error(t, err)
	require.Nil(t, got)
	req.EstimatedOutputTokens = 50
	req.EstimatedTokens = 951
	got, err = r.Route(t.Context(), req)
	require.Error(t, err)
	require.Nil(t, got)
	for _, rate := range []float64{-1, math.NaN(), math.Inf(1), math.Inf(-1)} {
		for _, m := range []providers.Model{{ID: "bad", CostIn: rate}, {ID: "bad", CostOut: rate}} {
			bad := NewSmartRouter(NewClassifier(DefaultThresholds()), []ProviderModels{{ProviderName: "bad", PricingKnown: map[string]bool{"bad": true}, Models: []providers.Model{m}}}, "")
			bad.SetCostPriority(true)
			result, err := bad.Route(t.Context(), RouteRequest{})
			require.Error(t, err)
			require.Nil(t, result)
		}
	}
	unknown := NewSmartRouter(NewClassifier(DefaultThresholds()), []ProviderModels{{ProviderName: "custom", Models: []providers.Model{{ID: "custom"}}}}, "")
	got, err = unknown.Route(t.Context(), RouteRequest{})
	require.NoError(t, err)
	require.False(t, got.CostEstimateKnown)
	require.Contains(t, got.Reason, "price unavailable")
	unknown.SetCostPriority(true)
	got, err = unknown.Route(t.Context(), RouteRequest{})
	require.ErrorContains(t, err, "known valid pricing")
	require.Nil(t, got)
	id, provider := unknown.CheapestModel(false, false)
	require.Empty(t, id)
	require.Empty(t, provider)
	free := NewSmartRouter(NewClassifier(DefaultThresholds()), []ProviderModels{{ProviderName: "explicit-free", PricingKnown: map[string]bool{"free": true}, Models: []providers.Model{{ID: "free"}}}}, "")
	free.SetCostPriority(true)
	got, err = free.Route(t.Context(), RouteRequest{})
	require.NoError(t, err)
	require.True(t, got.CostEstimateKnown)
	require.Zero(t, got.CostEstimate)
}

func TestCatalogPricingPresenceSurvivesFlattening(t *testing.T) {
	missingRoot := t.TempDir()
	writeModelTOML(t, missingRoot, "p/unknown.toml", "family = \"test\"\ncontext_window = 4096\n")
	_, err := models.Load(missingRoot)
	require.Error(t, err) // The authoritative catalog currently requires an explicit cost table.
	root := t.TempDir()
	writeModelTOML(t, root, "p/free.toml", "family = \"test\"\ncontext_window = 4096\nmax_output_tokens = 500\n[cost]\ninput = 0\noutput = 0\n")
	cat, err := models.Load(root)
	require.NoError(t, err)
	pms := ProviderModelsFromCatalog(cat)
	require.Len(t, pms, 1)
	require.True(t, pms[0].PricingKnown["p/free"])
	require.Equal(t, 500, pms[0].Models[0].MaxOutputTokens)
	r := NewSmartRouter(NewClassifier(DefaultThresholds()), pms, "")
	r.SetCostPriority(true)
	got, err := r.Route(t.Context(), RouteRequest{EstimatedTokens: 100, EstimatedOutputTokens: 100})
	require.NoError(t, err)
	require.Equal(t, "p/free", got.ModelID)
	require.True(t, got.CostEstimateKnown)
	require.Zero(t, got.CostEstimate)
}

func TestConcurrentCostPriorityToggleAndRoute(t *testing.T) {
	r := newTestRouter()
	var wg sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 100; n++ {
				r.SetCostPriority(n%2 == 0)
				_, err := r.Route(context.Background(), RouteRequest{UserInput: "refactor", CodeBlockCount: 3})
				if err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Wait()
}

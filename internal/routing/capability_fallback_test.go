package routing

import (
	"context"
	"testing"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/stretchr/testify/require"
)

func TestRequiredCapabilitiesNeverUseIncompatibleDefaults(t *testing.T) {
	models := []ProviderModels{{ProviderName: "text", Models: []providers.Model{{ID: "default"}}}}
	router := NewSmartRouter(NewClassifier(DefaultThresholds()), models, "default")
	for _, request := range []RouteRequest{
		{RequiredCapabilities: []string{"vision"}},
		{RequiredCapabilities: []string{"tools"}},
		{RequiredCapabilities: []string{"tools", "vision"}},
		{HasAttachments: true},
		{RequiredCapabilities: []string{"unrecognized-capability"}},
	} {
		result, err := router.Route(t.Context(), request)
		require.Error(t, err)
		require.Nil(t, result)
	}
	for _, caps := range [][2]bool{{true, false}, {false, true}, {true, true}} {
		id, provider := router.CheapestModel(caps[0], caps[1])
		require.Empty(t, id)
		require.Empty(t, provider)
	}
	for _, available := range [][]ProviderModels{nil, {{ProviderName: "", Models: []providers.Model{{ID: "default", SupportsTools: true}}}}, {{ProviderName: "configured", Models: []providers.Model{{ID: "", SupportsTools: true}}}}} {
		router := NewSmartRouter(NewClassifier(DefaultThresholds()), available, "default")
		result, err := router.Route(t.Context(), RouteRequest{RequiredCapabilities: []string{"tools"}})
		require.Error(t, err)
		require.Nil(t, result)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	result, err := router.Route(ctx, RouteRequest{})
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, result)
}

func TestCapabilityRoutingRetainsSelectedProviderForDuplicateModelIDs(t *testing.T) {
	models := []ProviderModels{
		{ProviderName: "expensive", Models: []providers.Model{{ID: "shared", CostIn: 10, SupportsTools: true, SupportsVision: true}}},
		{ProviderName: "cheap", Models: []providers.Model{{ID: "shared", CostIn: 1, SupportsTools: true, SupportsVision: true}}},
	}
	router := NewSmartRouter(NewClassifier(DefaultThresholds()), models, "shared")
	result, err := router.Route(t.Context(), RouteRequest{UserInput: "hi", RequiredCapabilities: []string{"tools", "vision"}})
	require.NoError(t, err)
	require.Equal(t, "cheap", result.Provider)
	require.True(t, result.CostEstimateKnown)
	require.InDelta(t, float64(1)/1_000_000, result.CostEstimate, 1e-15)
}

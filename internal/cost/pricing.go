package cost

import "github.com/Sahaj-Tech-ltd/overkill/internal/providers"

type CostBreakdown struct {
	InputCost  float64
	OutputCost float64
	CacheCost  float64
	Total      float64
}

func CalculateCost(usage providers.Usage, model providers.Model) float64 {
	return CalculateCostDetailed(usage, model).Total
}

func CalculateCostDetailed(usage providers.Usage, model providers.Model) CostBreakdown {
	input := float64(usage.InputTokens) * model.CostIn / 1_000_000
	output := float64(usage.OutputTokens) * model.CostOut / 1_000_000
	cache := float64(usage.CachedInputTokens) * model.CostCacheIn / 1_000_000
	return CostBreakdown{
		InputCost:  input,
		OutputCost: output,
		CacheCost:  cache,
		Total:      input + output + cache,
	}
}

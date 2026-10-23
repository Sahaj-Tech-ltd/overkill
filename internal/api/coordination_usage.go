package api

import (
	"context"
	"errors"
	"math"
	"sync/atomic"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/coordination"
	"github.com/Sahaj-Tech-ltd/overkill/internal/modelusage"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"github.com/google/uuid"
)

func (s *Server) coordinationUsageContext(ctx context.Context, message coordination.Message) (context.Context, *atomic.Bool) {
	incomplete := &atomic.Bool{}
	return modelusage.WithAccounting(ctx, modelusage.Accounting{
		Before: func(ctx context.Context) error { return s.coordinationStore.CheckTaskUsage(ctx, message.ID) },
		Record: func(ctx context.Context, model string, usage providers.Usage, reported bool) error {
			// Cancellation cannot discard usage already returned by a provider. Lease
			// fencing still applies on this bounded detached accounting transaction.
			recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
			defer cancel()
			price, known := s.coordinationModelCost(model, usage)
			err := s.coordinationStore.RecordTaskUsage(recordCtx, message.ID, uuid.NewString(), coordination.UsageSample{Usage: usage, Reported: reported, EstimatedCostUSD: price, PriceKnown: known})
			if err != nil && !errors.Is(err, coordination.ErrUsageBudget) {
				incomplete.Store(true)
			}
			return err
		},
	}), incomplete
}

// Configured prices are estimates, not invoices. Cached input is a subset of
// input tokens and must replace ordinary input pricing, never be added twice.
func (s *Server) coordinationModelCost(model string, usage providers.Usage) (float64, bool) {
	if s.cfg == nil {
		return 0, false
	}
	var result float64
	found := false
	for _, provider := range s.cfg.Providers {
		for _, entry := range provider.Models {
			if entry.ID != model {
				continue
			}
			if entry.CostIn < 0 || entry.CostOut < 0 || entry.CostCacheIn < 0 || math.IsNaN(entry.CostIn) || math.IsNaN(entry.CostOut) || math.IsNaN(entry.CostCacheIn) || math.IsInf(entry.CostIn, 0) || math.IsInf(entry.CostOut, 0) || math.IsInf(entry.CostCacheIn, 0) {
				return 0, false
			}
			if entry.CostIn == 0 && entry.CostOut == 0 && entry.CostCacheIn == 0 {
				return 0, false
			}
			cached := min(usage.CachedInputTokens, usage.InputTokens)
			if (cached > 0 && entry.CostCacheIn == 0) || (usage.InputTokens > cached && entry.CostIn == 0) || (usage.OutputTokens > 0 && entry.CostOut == 0) {
				return 0, false
			}
			price := float64(usage.InputTokens-cached)*entry.CostIn/1e6 + float64(cached)*entry.CostCacheIn/1e6 + float64(usage.OutputTokens)*entry.CostOut/1e6
			if math.IsNaN(price) || math.IsInf(price, 0) || price < 0 {
				return 0, false
			}
			if found && price != result {
				return 0, false
			}
			result = price
			found = true
		}
	}
	return result, found
}

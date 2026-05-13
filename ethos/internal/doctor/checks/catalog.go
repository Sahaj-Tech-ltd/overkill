package checks

import (
	"context"

	"github.com/Sahaj-Tech-ltd/overkill/internal/doctor"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

// RegisterCatalog hits models.dev (with a 5s timeout) and reports which tier
// answered: live (ok), cache (warn), baked (warn — offline fallback).
func RegisterCatalog(r *doctor.Runner, d Deps) {
	r.Register(doctor.SubsystemCheck{
		ID:       "catalog.modelsdev",
		Name:     "Models.dev catalog",
		Category: doctor.CatCore,
		Parallel: true,
		Fn: func(ctx context.Context) doctor.Result {
			cat, err := providers.FetchCatalog(ctx)
			if err != nil {
				return failf("check network access to models.dev",
					"FetchCatalog: %v", err)
			}
			switch cat.Source() {
			case providers.SourceLive:
				return okf("live catalog with %d providers", len(cat.Providers()))
			case providers.SourceCache:
				return warnf("models.dev unreachable; cached copy is in use",
					"using cached catalog (%d providers)", len(cat.Providers()))
			case providers.SourceBaked:
				return warnf("network is offline; baked-in catalog will go stale over time",
					"using baked catalog (%d providers)", len(cat.Providers()))
			default:
				return info("catalog source is unknown")
			}
		},
	})
}
